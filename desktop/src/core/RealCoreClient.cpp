#include "RealCoreClient.h"

#include "Logging.h"

#include <QHash>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QSysInfo>
#include <QTimer>
#include <QUuid>

#include <iterator>
#include <utility>

namespace {

// The core publishes a revision about four times a second: handshake ages and observer
// ticks change the snapshot even when nothing visible moved. Rebuilding the role model on
// every revision saturates the UI thread, which is what made the connect button feel
// frozen. This signature covers only what the UI renders.
QString snapshotSignature(const QVector<RoleSnapshot> &roles)
{
	QStringList parts;
	parts.reserve(roles.size());
	for (const RoleSnapshot &role : roles) {
		parts << role.id + QLatin1Char('|') + role.label + QLatin1Char('|') +
                 role.protocol + QLatin1Char('|') + role.server + QLatin1Char('|') +
                 role.state + QLatin1Char('|') + role.stateText + QLatin1Char('|') +
                 role.interfaceName + QLatin1Char('|') + role.lastError + QLatin1Char('|') +
                 role.availableActions.join(QLatin1Char(',')) + QLatin1Char('|') +
                 QString::number(role.pid) + QLatin1Char('|') +
                 QString::number(role.generation) + QLatin1Char('|') +
                 role.resolvedEndpoints.join(QLatin1Char(',')) + QLatin1Char('|') +
                 role.endpointLastError + QLatin1Char('|') +
                 QString::number(role.appliedUnderlayEpoch) + QLatin1Char('|') +
                 role.parkedPrefixes.join(QLatin1Char(',')) + QLatin1Char('|') +
                 QString::number(role.leshyIpv6) + QLatin1Char('|') +
                 QString::number(role.ipv4Configured) + QString::number(role.ipv4Ready) +
                 QLatin1Char('|') + role.ipv4Reason + QLatin1Char('|') +
                 QString::number(role.ipv6Configured) + QString::number(role.ipv6Ready) +
                 QLatin1Char('|') + role.ipv6Reason + QLatin1Char('|') +
                 QString::fromUtf8(QJsonDocument(QJsonObject::fromVariantMap(role.selectedRoutes))
                                       .toJson(QJsonDocument::Compact));
	}
	return parts.join(QLatin1Char('\n'));
}

} // namespace

namespace {
constexpr int apiVersion = 3;

QString displayState(QString value)
{
    if (value.compare(QStringLiteral("online"), Qt::CaseInsensitive) == 0 ||
        value.compare(QStringLiteral("ready"), Qt::CaseInsensitive) == 0)
        return QStringLiteral("Ready");
    if (value.compare(QStringLiteral("connecting"), Qt::CaseInsensitive) == 0 ||
        value.compare(QStringLiteral("starting"), Qt::CaseInsensitive) == 0)
        return QStringLiteral("Connecting");
    if (value.compare(QStringLiteral("degraded"), Qt::CaseInsensitive) == 0 ||
        value.compare(QStringLiteral("recovering"), Qt::CaseInsensitive) == 0)
        return QStringLiteral("Recovering");
    if (value.compare(QStringLiteral("failed"), Qt::CaseInsensitive) == 0)
        return QStringLiteral("Failed");
    if (value.compare(QStringLiteral("stopped"), Qt::CaseInsensitive) == 0)
        return QStringLiteral("Stopped");
    return value.isEmpty() ? QStringLiteral("Connecting") : value;
}
}

RealCoreClient::RealCoreClient(QObject *parent)
    : RealCoreClient(defaultSocketPath(), parent)
{
}

RealCoreClient::RealCoreClient(const QString &socketPath, QObject *parent)
    : CoreBackend(parent)
    , socket(new CoreSocket(this))
    , m_socketPath(socketPath)
    , m_platformName(QSysInfo::productType())
{
    connect(socket, &CoreSocket::connected, this, &RealCoreClient::sendHandshake);
    connect(socket, &CoreSocket::readyRead, this, &RealCoreClient::processIncomingMessage);
    connect(socket, &CoreSocket::disconnected, this, &RealCoreClient::setDisconnected);
    connect(socket, &CoreSocket::errorOccurred, this, [this]() {
        m_coreState = QStringLiteral("Unavailable");
        // Surface why the core is unreachable: a bare "Unavailable" hides the
        // one thing the operator can fix (the kikimora group membership on
        // Linux, the service being down on either platform).
        const QString reason = socket->lastError();
        if (!reason.isEmpty()) {
            m_lastCommandError = QStringLiteral("core socket: %1").arg(reason);
        }
        emit snapshotChanged();
        // A refused initial dial emits errorOccurred only (no disconnected
        // signal): the UI must retry here too, or "UI started before the
        // core" - and the autostart race that produces it - leaves the UI
        // permanently Unavailable.
        scheduleReconnect();
    });
    connectToCore();
}

RealCoreClient::~RealCoreClient()
{
    // Teardown can happen while QQmlApplicationEngine is already releasing
    // its bindings. Do not let abort() synchronously deliver socket callbacks
    // into the half-destroyed UI/client graph.
    socket->blockSignals(true);
    socket->abort();
}

QString RealCoreClient::defaultSocketPath()
{
#ifdef Q_OS_WIN
    // The Windows service publishes its control socket on the same AF_UNIX path.
    return QStringLiteral("C:/ProgramData/Kikimora/core.sock");
#else
    return QStringLiteral("/run/kikimora/core.sock");
#endif
}

bool RealCoreClient::isSocketAvailable(const QString &socketPath, int timeoutMs)
{
    CoreSocket probe;
    probe.connectToServer(socketPath);
    const bool connected = probe.waitForConnected(timeoutMs);
    probe.abort();
    return connected;
}

QString RealCoreClient::backendKind() const { return QStringLiteral("real"); }
QString RealCoreClient::platformName() const { return m_platformName; }
QString RealCoreClient::coreState() const { return m_coreState; }
QString RealCoreClient::activeProfile() const { return m_activeProfile; }
QStringList RealCoreClient::profiles() const { return m_profiles; }
QString RealCoreClient::underlaySummary() const { return m_underlaySummary; }
QVariantMap RealCoreClient::underlay() const { return m_underlay; }
QString RealCoreClient::lastCommandError() const { return m_lastCommandError; }
QString RealCoreClient::lastCommandMessage() const { return m_commandMessage; }
QString RealCoreClient::aggregateState() const { return m_aggregateState; }

QString RealCoreClient::aggregateStateText() const
{
    // Managed authority is the operator-safety truth: a managed core that
    // lost its route authority must never present as Protected, whatever the
    // tunnel aggregate says (P0-UI1).
    if (m_managedMode && !m_routeAuthorityReady)
        return QStringLiteral("Routing authority lost");
    if (m_aggregateState == QStringLiteral("Ready")) return QStringLiteral("Protected");
    if (m_aggregateState == QStringLiteral("Connecting")) return QStringLiteral("Connecting all tunnels…");
    if (m_aggregateState == QStringLiteral("WaitingForUnderlay")) return QStringLiteral("Waiting for physical network");
    if (m_aggregateState == QStringLiteral("Recovering")) return QStringLiteral("Partially protected · recovering");
    // The core reports PartiallyReady when some desired roles are already up
    // (toad/internal/core/snapshot.go). Falling through to "Not connected"
    // used to hide live tunnels behind a disconnected label.
    if (m_aggregateState == QStringLiteral("PartiallyReady")) return QStringLiteral("Partially protected");
    if (m_aggregateState == QStringLiteral("Failed") || m_aggregateState == QStringLiteral("Degraded"))
        return QStringLiteral("Protection degraded");
    return QStringLiteral("Not connected");
}

QString RealCoreClient::aggregateActionText() const
{
    // The label must state what the button will actually do. Every aggregate
    // except the settled terminal ones still wants tunnels, so disconnect-all
    // is the honest action there. "Recovering" and "PartiallyReady" previously
    // fell through to CONNECT, which the core treats as a no-op because every
    // role is already enabled (control.go ConnectAll bumps the operation only
    // for roles that were disabled) — the button looked dead.
    if (m_aggregateState.isEmpty() ||
        m_aggregateState == QStringLiteral("Stopped") ||
        m_aggregateState == QStringLiteral("Failed"))
        return QStringLiteral("CONNECT");
    return QStringLiteral("DISCONNECT");
}

qulonglong RealCoreClient::revision() const { return m_revision; }
bool RealCoreClient::leshySupported() const { return m_leshySupported; }
RoleListModel *RealCoreClient::roles() { return &m_roles; }

// The channel is actionable only when this connection completed a compatible
// handshake AND an authoritative snapshot arrived AND the core finished its
// own startup AND no mode transition is in flight.
bool RealCoreClient::commandChannelReady() const
{
    return m_apiCompatible && m_authoritativeSnapshot && m_startupReady &&
           m_modeTransition == QStringLiteral("none");
}

bool RealCoreClient::safeToMutate() const
{
    return commandChannelReady() && (!m_managedMode || m_routeAuthorityReady);
}

bool RealCoreClient::diagnosticsMutable() const
{
    return m_apiCompatible && m_startupReady && m_modeTransition == QStringLiteral("none");
}

RealCoreClient::CommandClass RealCoreClient::commandClass(const QString &method)
{
    static const QHash<QString, CommandClass> classes{
        {QStringLiteral("ConnectAll"), CommandClass::Mutate},
        {QStringLiteral("ConnectRole"), CommandClass::Mutate},
        {QStringLiteral("RetryRole"), CommandClass::Mutate},
        {QStringLiteral("ValidateRole"), CommandClass::Mutate},
        {QStringLiteral("RediscoverEndpoints"), CommandClass::Mutate},
        {QStringLiteral("SetActiveProfile"), CommandClass::Mutate},
        {QStringLiteral("CreateTunnel"), CommandClass::Mutate},
        {QStringLiteral("CreateProfile"), CommandClass::Mutate},
        {QStringLiteral("SetTunnelProfiles"), CommandClass::Mutate},
        {QStringLiteral("RemoveTunnel"), CommandClass::Mutate},
        {QStringLiteral("SetRoutingList"), CommandClass::Mutate},
        {QStringLiteral("SetBypassMode"), CommandClass::Mutate},
        {QStringLiteral("SetRoleIPv6"), CommandClass::Mutate},
        {QStringLiteral("DisconnectAll"), CommandClass::Disconnect},
        {QStringLiteral("DisconnectRole"), CommandClass::Disconnect},
        {QStringLiteral("GetDiagnosticsSnapshot"), CommandClass::DiagnosticsRead},
        {QStringLiteral("GetRoutingLists"), CommandClass::DiagnosticsRead},
        {QStringLiteral("SetDiagnosticsCapture"), CommandClass::DiagnosticsMutate},
    };
    return classes.value(method, CommandClass::Channel);
}

bool RealCoreClient::commandAllowed(CommandClass cls) const
{
    switch (cls) {
    case CommandClass::Mutate: return safeToMutate();
    case CommandClass::Disconnect: return safeToDisconnect();
    case CommandClass::DiagnosticsRead: return diagnosticsAvailable();
    case CommandClass::DiagnosticsMutate: return diagnosticsMutable();
    case CommandClass::Channel: break;
    }
    return true;
}

void RealCoreClient::reportBlocked(const QString &method)
{
    QString reason;
    if (!m_apiCompatible)
        reason = QStringLiteral("the core API is not compatible");
    else if (!m_authoritativeSnapshot || !m_startupReady)
        reason = QStringLiteral("the core has not finished starting");
    else if (m_modeTransition != QStringLiteral("none"))
        reason = QStringLiteral("a mode transition is in progress");
    else if (m_managedMode && !m_routeAuthorityReady)
        reason = QStringLiteral("routing authority is unavailable");
    else
        reason = QStringLiteral("the core is unreachable");
    m_lastCommandError = QStringLiteral("%1 blocked: %2").arg(method, reason);
    uiLog(QStringLiteral("blocked %1").arg(method));
    emit snapshotChanged();
}

void RealCoreClient::toggleAll()
{
    aggregateActionText() == QStringLiteral("DISCONNECT") ? disconnectAll() : connectAll();
}

void RealCoreClient::connectAll() { sendCommand(QStringLiteral("ConnectAll")); }
void RealCoreClient::disconnectAll() { sendCommand(QStringLiteral("DisconnectAll")); }

void RealCoreClient::roleAction(int row)
{
    const RoleSnapshot role = m_roles.roleAt(row);
    if (role.id.isEmpty()) return;
    if (!role.availableActions.isEmpty())
        roleActionByName(row, role.availableActions.first());
    else
        roleActionByName(row, role.state == QStringLiteral("Stopped") ? QStringLiteral("connect")
                                                                      : QStringLiteral("disconnect"));
}

void RealCoreClient::roleActionByName(int row, const QString &action)
{
    const RoleSnapshot role = m_roles.roleAt(row);
    if (role.id.isEmpty()) return;
    static const QHash<QString, QString> methods{
        {QStringLiteral("connect"), QStringLiteral("ConnectRole")},
        {QStringLiteral("disconnect"), QStringLiteral("DisconnectRole")},
        {QStringLiteral("retry"), QStringLiteral("RetryRole")},
        {QStringLiteral("validate"), QStringLiteral("ValidateRole")},
        {QStringLiteral("rediscover"), QStringLiteral("RediscoverEndpoints")},
    };
    const QString method = methods.value(action);
    if (method.isEmpty()) return;
    sendCommand(method, {{QStringLiteral("role"), role.id}});
}

void RealCoreClient::retryRole(int row)
{
    const RoleSnapshot role = m_roles.roleAt(row);
    if (!role.id.isEmpty() && role.availableActions.contains(QStringLiteral("retry")))
        sendCommand(QStringLiteral("RetryRole"), {{QStringLiteral("role"), role.id}});
}

void RealCoreClient::validateRole(int row)
{
    const RoleSnapshot role = m_roles.roleAt(row);
    if (!role.id.isEmpty()) sendCommand(QStringLiteral("ValidateRole"), {{QStringLiteral("role"), role.id}});
}

void RealCoreClient::setRoleIpv6(int row, bool enabled)
{
    const RoleSnapshot role = m_roles.roleAt(row);
    if (role.id.isEmpty()) return;
    sendCommand(QStringLiteral("SetRoleIPv6"),
                {{QStringLiteral("role"), role.id}, {QStringLiteral("enabled"), enabled}});
}

void RealCoreClient::rediscoverEndpoints(int row)
{
    const RoleSnapshot role = m_roles.roleAt(row);
    if (!role.id.isEmpty()) sendCommand(QStringLiteral("RediscoverEndpoints"), {{QStringLiteral("role"), role.id}});
}

void RealCoreClient::nextDemoScenario() {}

void RealCoreClient::createTunnel(const QVariantMap &spec)
{
    m_commandMessage.clear();
    sendCommand(QStringLiteral("CreateTunnel"),
                {{QStringLiteral("tunnel"), QJsonObject::fromVariantMap(spec)}});
}

void RealCoreClient::createProfile(const QString &name, const QStringList &roles)
{
    m_commandMessage.clear();
    m_pendingProfileName = name.trimmed();
    sendCommand(QStringLiteral("CreateProfile"),
                {{QStringLiteral("profile"), m_pendingProfileName},
                 {QStringLiteral("roles"), QJsonArray::fromStringList(roles)}},
                QStringLiteral("createprofile"));
}

void RealCoreClient::setTunnelProfiles(const QString &name, const QStringList &profiles)
{
    // Test the safety guard before touching the local membership cache: a
    // blocked edit must leave the optimistic state exactly as it was.
    if (!commandAllowed(commandClass(QStringLiteral("SetTunnelProfiles")))) {
        reportBlocked(QStringLiteral("SetTunnelProfiles"));
        return;
    }
    m_commandMessage.clear();
    m_roleProfiles[name] = profiles;
    sendCommand(QStringLiteral("SetTunnelProfiles"),
                {{QStringLiteral("role"), name}, {QStringLiteral("profiles"), QJsonArray::fromStringList(profiles)}});
}

QStringList RealCoreClient::profilesForRole(const QString &name) const
{
    return m_roleProfiles.value(name);
}

QVariantMap RealCoreClient::routingLists() const
{
    return m_routingLists;
}

QVariantList RealCoreClient::routingZones() const
{
    return m_routingZones;
}

QString RealCoreClient::bypassMode() const
{
    return m_bypassMode;
}

QStringList RealCoreClient::roleLabels() const
{
    return m_roleLabels;
}

void RealCoreClient::setBypassMode(const QString &mode)
{
    if (!commandAllowed(CommandClass::Mutate)) {
        reportBlocked(QStringLiteral("SetBypassMode"));
        return;
    }
    m_commandMessage.clear();
    // The reply carries a message, not the stored rule: read the lists back so
    // the row shows what the core actually saved.
    m_resyncRoutingLists = true;
    sendCommand(QStringLiteral("SetBypassMode"), {{QStringLiteral("mode"), mode}});
}

void RealCoreClient::refreshRoutingLists()
{
    sendCommand(QStringLiteral("GetRoutingLists"));
}

void RealCoreClient::saveRoutingList(const QString &name, const QString &content)
{
    if (!commandAllowed(CommandClass::Mutate)) {
        reportBlocked(QStringLiteral("SetRoutingList"));
        return;
    }
    m_commandMessage.clear();
    m_resyncRoutingLists = true;
    sendCommand(QStringLiteral("SetRoutingList"),
                {{QStringLiteral("list"), name}, {QStringLiteral("content"), content}});
}

void RealCoreClient::removeTunnel(const QString &name)
{
    m_commandMessage.clear();
    sendCommand(QStringLiteral("RemoveTunnel"), {{QStringLiteral("role"), name}});
}

void RealCoreClient::importTunnel(const QString &text)
{
    const TunnelImport parsed = ParseTunnelImport(text);
    if (parsed.spec.isEmpty()) {
        m_commandMessage = parsed.error;
        emit snapshotChanged();
        return;
    }
    createTunnel(parsed.spec);
}

void RealCoreClient::setActiveProfile(const QString &name)
{
    if (name.isEmpty())
        return;
    // A blocked switch must not touch the optimistic profile value: test the
    // guard before the local cache moves (roadmap §8.1).
    if (!commandAllowed(commandClass(QStringLiteral("SetActiveProfile")))) {
        reportBlocked(QStringLiteral("SetActiveProfile"));
        return;
    }
    // The switch is a context change: reflect it at once instead of freezing
    // on the core's round-trip (the win10 VM measured a 5.6s SetActiveProfile
    // while roles churned, and the operator re-clicked). The core's answer or
    // the next streamed snapshot confirms the value; an error triggers a
    // resync so a rejected switch cannot linger.
    if (m_profiles.contains(name) && m_activeProfile != name) {
        m_profileSwitchFallback = m_activeProfile;
        m_pendingProfileSwitch = true;
        m_activeProfile = name;
        emit snapshotChanged();
    }
    sendCommand(QStringLiteral("SetActiveProfile"),
                {{QStringLiteral("profile"), name}}, QStringLiteral("profileswitch"));
}

void RealCoreClient::connectToCore()
{
    uiLog(QStringLiteral("connect %1").arg(m_socketPath));
    if (socket->state() != QLocalSocket::UnconnectedState) return;
    m_coreState = QStringLiteral("Connecting");
    emit snapshotChanged();
    socket->connectToServer(m_socketPath);
}

void RealCoreClient::setDisconnected()
{
    m_coreState = QStringLiteral("Unavailable");
    // The manager's revision counter restarts at 1 in every core process.
    // Keeping the pre-restart value here dropped every reconnected snapshot
    // until the new process caught up - the UI froze on the dead core's last
    // state (for example "Protected") for minutes after a service restart.
    m_revision = 0;
    m_roleSignature.clear();
    // Safety truth never survives the socket: the flags describe one core
    // connection, and a stale route_authority_ready=true must not keep
    // mutation enabled across a reconnect.
    m_apiCompatible = false;
    m_capabilities.clear();
    m_authoritativeSnapshot = false;
    m_startupReady = false;
    m_managedMode = false;
    m_modeGeneration = 0;
    m_activationIdFingerprint.clear();
    m_modeTransition = QStringLiteral("none");
    m_modeTransitionIdFingerprint.clear();
    m_routeAuthorityReady = false;
    m_routeAuthorityError.clear();
    // A pending resync can never complete now: the new core's revision
    // restarts at 1, and the gap detector requires m_revision != 0. Clear
    // the flag so the handshake GetSnapshot applies instead of being dropped.
    m_resyncPending = false;
    // A profile creation waiting for the core's answer can never receive it
    // now: resolve it, or the create sheet stays busy for ever.
    if (!m_pendingProfileName.isEmpty()) {
        emit profileCreationFinished(m_pendingProfileName, false, QStringLiteral("connection lost"));
        m_pendingProfileName.clear();
    }
    m_pendingProfileSwitch = false;
    emit snapshotChanged();
    scheduleReconnect();
}

void RealCoreClient::scheduleReconnect()
{
    static constexpr int delays[] = {500, 1000, 2000, 5000, 10000};
    const int index = qMin(m_reconnectAttempt, static_cast<int>(std::size(delays) - 1));
    ++m_reconnectAttempt;
    QTimer::singleShot(delays[index], this, &RealCoreClient::connectToCore);
}

void RealCoreClient::sendHandshake()
{
    sendMessage({{QStringLiteral("version"), apiVersion},
                  {QStringLiteral("id"), QStringLiteral("handshake")},
                  {QStringLiteral("method"), QStringLiteral("Handshake")} });
}

void RealCoreClient::sendGetSnapshot(const QString &id)
{
    sendMessage({{QStringLiteral("version"), apiVersion},
                  {QStringLiteral("id"), id},
                  {QStringLiteral("method"), QStringLiteral("GetSnapshot")} });
}

void RealCoreClient::sendSubscribe()
{
    sendMessage({{QStringLiteral("version"), apiVersion},
                  {QStringLiteral("id"), QStringLiteral("subscribe")},
                  {QStringLiteral("method"), QStringLiteral("Subscribe")} });
}

void RealCoreClient::refreshDiagnostics()
{
    sendCommand(QStringLiteral("GetDiagnosticsSnapshot"));
}

void RealCoreClient::setDiagnosticsCapture(bool enabled)
{
    // Guard before the trailing refresh: a blocked toggle must not send any
    // frame, including the diagnostics refresh that normally follows it.
    if (!commandAllowed(CommandClass::DiagnosticsMutate)) {
        reportBlocked(QStringLiteral("SetDiagnosticsCapture"));
        return;
    }
    // Set then refresh over the same connection: the core handles requests in
    // order, so the follow-up snapshot already reports the new switch.
    sendCommand(QStringLiteral("SetDiagnosticsCapture"),
                QJsonObject{{QStringLiteral("enabled"), enabled}});
    refreshDiagnostics();
}

void RealCoreClient::sendCommand(const QString &method, const QJsonObject &params,
                                 const QString &id)
{
    // The backend enforces the safety policy itself: disabled QML buttons are
    // not the only guard, a programmatic call is rejected by class before any
    // frame leaves the process (roadmap §8.1).
    if (!commandAllowed(commandClass(method))) {
        reportBlocked(method);
        return;
    }
    // A new user command supersedes the previous command-scoped error; the
    // reply below sets a fresh one when it fails. Streamed snapshots never
    // touch this field, so an unrelated push cannot resurrect or clear it.
    m_lastCommandError.clear();
    QJsonObject request{{QStringLiteral("version"), apiVersion},
                        {QStringLiteral("id"), id.isEmpty()
                                                    ? QUuid::createUuid().toString(QUuid::WithoutBraces)
                                                    : id},
                        {QStringLiteral("method"), method}};
    for (auto it = params.begin(); it != params.end(); ++it) request.insert(it.key(), it.value());
    sendMessage(request);
}

void RealCoreClient::sendMessage(const QJsonObject &message)
{
    const QByteArray payload = QJsonDocument(message).toJson(QJsonDocument::Compact);
    if (payload.isEmpty() || payload.size() > 1024 * 1024 || socket->state() != QLocalSocket::ConnectedState)
        return;
    QByteArray frame(4, Qt::Uninitialized);
    const quint32 size = static_cast<quint32>(payload.size());
    frame[0] = static_cast<char>((size >> 24) & 0xff);
    frame[1] = static_cast<char>((size >> 16) & 0xff);
    frame[2] = static_cast<char>((size >> 8) & 0xff);
    frame[3] = static_cast<char>(size & 0xff);
    frame += payload;
    socket->write(frame);
    socket->flush();
}

void RealCoreClient::processIncomingMessage()
{
    m_buffer += socket->readAll();
    while (m_buffer.size() >= 4) {
        const quint32 size = (static_cast<quint8>(m_buffer[0]) << 24) |
                             (static_cast<quint8>(m_buffer[1]) << 16) |
                             (static_cast<quint8>(m_buffer[2]) << 8) |
                             static_cast<quint8>(m_buffer[3]);
        if (size == 0 || size > 1024 * 1024) {
            socket->abort();
            return;
        }
        if (m_buffer.size() < 4 + static_cast<int>(size)) return;
        const QByteArray payload = m_buffer.mid(4, size);
        m_buffer.remove(0, 4 + size);
        QJsonParseError parseError;
        const QJsonDocument document = QJsonDocument::fromJson(payload, &parseError);
        if (parseError.error != QJsonParseError::NoError || !document.isObject()) continue;
        const QJsonObject object = document.object();
        uiLog(QStringLiteral("recv %1").arg(QString::fromUtf8(payload)));
        if (object.contains(QStringLiteral("lists"))) {
            m_routingLists = object.value(QStringLiteral("lists")).toObject().toVariantMap();
            m_routingZones = object.value(QStringLiteral("zones")).toArray().toVariantList();
            m_bypassMode = object.value(QStringLiteral("bypass_mode")).toString(QStringLiteral("direct"));
            emit snapshotChanged();
        }
        if (object.contains(QStringLiteral("message"))) {
            m_commandMessage = object.value(QStringLiteral("message")).toString();
            emit snapshotChanged();
        }
        if (m_resyncRoutingLists && !object.contains(QStringLiteral("lists"))) {
            // The save answered with a message only: ask for the lists so the page shows what
            // the core actually stored.
            m_resyncRoutingLists = false;
            sendCommand(QStringLiteral("GetRoutingLists"));
        }
        if (object.contains(QStringLiteral("observers"))) {
            m_observers = object.value(QStringLiteral("observers")).toObject().toVariantMap();
            emit snapshotChanged();
        }
        if (object.contains(QStringLiteral("diagnostics"))) {
            m_diagnostics = object.value(QStringLiteral("diagnostics")).toObject().toVariantMap();
            emit snapshotChanged();
        }
        const int responseVersion = object.value(QStringLiteral("version")).toInt();
        if (responseVersion != 1 && responseVersion != apiVersion) continue;
        const QString id = object.value(QStringLiteral("id")).toString();
        if (!object.value(QStringLiteral("ok")).toBool()) {
            const QJsonObject detail = object.value(QStringLiteral("error_detail")).toObject();
            const QJsonObject error = object.value(QStringLiteral("error")).toObject();
            m_lastCommandError = detail.value(QStringLiteral("message")).toString(
                error.value(QStringLiteral("message")).toString(object.value(QStringLiteral("error")).toString()));
            emit snapshotChanged();
            if (id == QStringLiteral("createprofile")) {
                emit profileCreationFinished(m_pendingProfileName, false, m_lastCommandError);
                m_pendingProfileName.clear();
            }
            if (id == QStringLiteral("profileswitch") && m_pendingProfileSwitch) {
                // The core rejected the switch: roll the optimistic value back to
                // the profile that was active when the switch was requested.
                m_pendingProfileSwitch = false;
                m_activeProfile = m_profileSwitchFallback;
                emit snapshotChanged();
            }
            continue;
        }
        if (id == QStringLiteral("profileswitch"))
            m_pendingProfileSwitch = false;
        if (id == QStringLiteral("createprofile")) {
            emit profileCreationFinished(m_pendingProfileName, true,
                                         object.value(QStringLiteral("message")).toString());
            m_pendingProfileName.clear();
        }
        if (id == QStringLiteral("handshake")) {
            // Validate the offered protocol window before declaring Connected
            // (roadmap §8): an incompatible core becomes an explicit state,
            // not an endless reconnect loop of identical negotiations.
            const int minVersion = object.value(QStringLiteral("min_version")).toInt(1);
            const int maxVersion = object.value(QStringLiteral("max_version")).toInt(0);
            if (apiVersion < minVersion || apiVersion > maxVersion) {
                m_apiCompatible = false;
                m_capabilities.clear();
                m_coreState = QStringLiteral("Incompatible");
                m_lastCommandError =
                    QStringLiteral("core API range %1..%2 does not support client API %3")
                        .arg(QString::number(minVersion), QString::number(maxVersion),
                             QString::number(apiVersion));
                uiLog(m_lastCommandError);
                emit snapshotChanged();
                continue;
            }
            m_apiCompatible = true;
            m_capabilities.clear();
            for (const QJsonValue &value : object.value(QStringLiteral("capabilities")).toArray())
                m_capabilities.append(value.toString());
            m_reconnectAttempt = 0;
            m_coreState = QStringLiteral("Connected");
            emit snapshotChanged();
            sendGetSnapshot();
            sendSubscribe();
            // The default-route rule lives beside the routing lists, not in the
            // snapshot: without this request the row would keep its initial
            // "direct" and claim a rule the core does not have.
            refreshRoutingLists();
        }
        if (object.value(QStringLiteral("snapshot")).isObject())
            handleSnapshot(object.value(QStringLiteral("snapshot")).toObject(), id == QStringLiteral("subscribe"), id);
    }
}

void RealCoreClient::handleSnapshot(const QJsonObject &snapshot, bool streamed, const QString &requestId)
{
    const qulonglong nextRevision = snapshot.value(QStringLiteral("revision")).toVariant().toULongLong();
    // A resync answer ends the wait even when it is not newer than the applied state: that
    // means a later streamed snapshot already covered the gap. Keeping the flag set here
    // used to freeze every further update after a burst of revisions.
    if (requestId == QStringLiteral("resync"))
        m_resyncPending = false;
    if (nextRevision == 0 || nextRevision <= m_revision) return;
    // A command response can arrive between a gap notification and the
    // authoritative resync response. Do not let it consume the resync slot.
    if (m_resyncPending && requestId != QStringLiteral("resync")) return;
    if (streamed && m_revision != 0 && nextRevision > m_revision + 1 && !m_resyncPending) {
        m_resyncPending = true;
        sendGetSnapshot(QStringLiteral("resync"));
        return;
    }
    if (requestId == QStringLiteral("resync")) m_resyncPending = false;
    m_coreState = snapshot.value(QStringLiteral("core_state")).toString();
    m_activeProfile = snapshot.value(QStringLiteral("active_profile")).toString();
    m_profiles.clear();
    for (const QJsonValue &value : snapshot.value(QStringLiteral("profiles")).toArray()) {
        const QString profile = value.toString();
        if (!profile.isEmpty()) m_profiles.append(profile);
    }
    m_underlaySummary = snapshot.value(QStringLiteral("underlay_summary")).toString();
    const QJsonObject underlay = snapshot.value(QStringLiteral("underlay")).toObject();
    if (!underlay.isEmpty()) {
        m_underlay.clear();
        m_underlay.insert(QStringLiteral("epoch"), underlay.value(QStringLiteral("epoch")).toVariant());
        m_underlay.insert(QStringLiteral("available"), underlay.value(QStringLiteral("ipv4")).isObject() || underlay.value(QStringLiteral("ipv6")).isObject());
        m_underlaySummary = underlay.value(QStringLiteral("last_change_reason")).toString(m_underlaySummary);
    }
    m_aggregateState = snapshot.value(QStringLiteral("aggregate_state")).toString();
    m_leshySupported = snapshot.value(QStringLiteral("leshy_supported")).toBool(false);
    // Managed routing authority truth (D9/P0-UI1): the aggregate state alone
    // must never drive the safety presentation.
    m_startupReady = snapshot.value(QStringLiteral("startup_ready")).toBool(false);
    m_managedMode = snapshot.value(QStringLiteral("managed_mode")).toBool(false);
    m_modeGeneration = snapshot.value(QStringLiteral("mode_generation")).toVariant().toULongLong();
    m_activationIdFingerprint = snapshot.value(QStringLiteral("activation_id_fingerprint")).toString();
    m_modeTransition = snapshot.value(QStringLiteral("mode_transition")).toString();
    if (m_modeTransition.isEmpty()) m_modeTransition = QStringLiteral("none");
    m_modeTransitionIdFingerprint =
        snapshot.value(QStringLiteral("mode_transition_id_fingerprint")).toString();
    m_routeAuthorityReady = snapshot.value(QStringLiteral("route_authority_ready")).toBool(false);
    m_routeAuthorityError = snapshot.value(QStringLiteral("route_authority_error")).toString();
    // This snapshot completes the handshake on the current connection: the
    // channel becomes actionable here, and a transport error from the dead
    // previous connection is resolved. Streamed pushes take the same path for
    // state, but only this first authoritative fetch clears the error.
    m_authoritativeSnapshot = true;
    if (requestId == QStringLiteral("getsnapshot"))
        m_lastCommandError.clear();
    m_revision = nextRevision;
    updateModelFromSnapshot(snapshot);
    emit snapshotChanged();
}

void RealCoreClient::updateModelFromSnapshot(const QJsonObject &snapshot)
{
    QVector<RoleSnapshot> mapped;
    for (const QJsonValue &value : snapshot.value(QStringLiteral("roles")).toArray()) {
        const QJsonObject object = value.toObject();
        RoleSnapshot role;
        role.id = object.value(QStringLiteral("id")).toString();
        role.label = object.value(QStringLiteral("label")).toString();
        role.protocol = object.value(QStringLiteral("protocol")).toString();
        role.pid = object.value(QStringLiteral("pid")).toInt(-1);
        role.generation = object.value(QStringLiteral("generation")).toVariant().toULongLong();
        const QString rawState = object.value(QStringLiteral("state")).toString();
        role.state = snapshot.value(QStringLiteral("schema")).toInt(1) >= 2 ? rawState : displayState(rawState);
        role.stateText = displayState(rawState);
        role.lastError = object.value(QStringLiteral("reason")).toString();
        if (role.lastError.isEmpty()) role.lastError = QStringLiteral("—");
        const QJsonObject iface = object.value(QStringLiteral("interface")).toObject();
        role.interfaceName = iface.value(QStringLiteral("name")).toString();
        role.interfaceIndex = iface.value(QStringLiteral("ifindex")).toInt(-1);
        role.interfaceMtu = iface.value(QStringLiteral("mtu")).toInt(-1);
        const QJsonObject session = object.value(QStringLiteral("session")).toObject();
        role.sessionConnected = session.value(QStringLiteral("connected")).toBool(false);
        role.sessionRxBytes = session.value(QStringLiteral("rx_bytes")).toDouble(0);
        role.sessionTxBytes = session.value(QStringLiteral("tx_bytes")).toDouble(0);
        role.sessionEndpoint = session.value(QStringLiteral("endpoint")).toString();
        role.lastHandshakeAgeMs = session.contains(QStringLiteral("last_handshake_age_ms"))
            ? session.value(QStringLiteral("last_handshake_age_ms")).toVariant().toLongLong() : -1;
        role.routeReady = object.value(QStringLiteral("route_ready")).toBool(role.routeReady);
        // Independent per-family readiness (P0-UI2): never derived from
        // route_ready, which only means "at least one configured family works".
        const QJsonObject families = object.value(QStringLiteral("families")).toObject();
        const QJsonObject ipv4 = families.value(QStringLiteral("ipv4")).toObject();
        role.ipv4Configured = ipv4.value(QStringLiteral("configured")).toBool(false);
        role.ipv4Ready = ipv4.value(QStringLiteral("ready")).toBool(false);
        role.ipv4Reason = ipv4.value(QStringLiteral("reason")).toString();
        const QJsonObject ipv6 = families.value(QStringLiteral("ipv6")).toObject();
        role.ipv6Configured = ipv6.value(QStringLiteral("configured")).toBool(false);
        role.ipv6Ready = ipv6.value(QStringLiteral("ready")).toBool(false);
        role.ipv6Reason = ipv6.value(QStringLiteral("reason")).toString();
        // Managed selected-route counters (P0-UI3) pass through as the core
        // publishes them; diagnostics render the map, Home stays a summary.
        if (object.value(QStringLiteral("selected_routes")).isObject())
            role.selectedRoutes =
                object.value(QStringLiteral("selected_routes")).toObject().toVariantMap();
        role.desiredEnabled = object.value(QStringLiteral("desired_enabled")).toBool(false) || object.value(QStringLiteral("desired_state")).toString() == QStringLiteral("enabled");
        role.operation = object.value(QStringLiteral("operation")).toVariant().toULongLong();
        role.validatedUnderlayEpoch = object.value(QStringLiteral("validated_underlay_epoch")).toVariant().toULongLong();
        const QJsonObject endpoint = object.value(QStringLiteral("endpoint")).toObject();
        role.endpointState = endpoint.value(QStringLiteral("state")).toString(role.endpointState);
        role.endpointLastError = endpoint.value(QStringLiteral("last_error")).toString();
        role.appliedUnderlayEpoch = endpoint.value(QStringLiteral("applied_underlay_epoch")).toVariant().toULongLong();
        for (const QJsonValue &value : endpoint.value(QStringLiteral("configured")).toArray()) role.configuredEndpoints.append(value.toString());
        for (const QJsonValue &value : endpoint.value(QStringLiteral("live")).toArray()) role.liveEndpoints.append(value.toString());
        for (const QJsonValue &value : endpoint.value(QStringLiteral("resolved")).toArray()) role.resolvedEndpoints.append(value.toString());
        const QJsonObject publication = object.value(QStringLiteral("publication")).toObject();
        role.leshyPublished = publication.value(QStringLiteral("published")).toBool(role.leshyPublished);
        role.leshyZone = publication.value(QStringLiteral("zone")).toString();
        role.leshyIpv6 = object.value(QStringLiteral("leshy_ipv6")).toBool(true);
        const QJsonObject parking = object.value(QStringLiteral("parking")).toObject();
        role.parkingActive = parking.value(QStringLiteral("active")).toBool(false);
        role.parkedRoutes = parking.value(QStringLiteral("count")).toInt(role.parkedRoutes);
        for (const QJsonValue &value : parking.value(QStringLiteral("prefixes")).toArray())
            role.parkedPrefixes.append(value.toString());
        const QJsonObject recovery = object.value(QStringLiteral("recovery")).toObject();
        role.recoveryAction = recovery.value(QStringLiteral("step")).toString();
        role.recoveryReason = recovery.value(QStringLiteral("last_error")).toString();
        role.recoveryAttempt = recovery.value(QStringLiteral("attempt")).toInt(0);
        role.nextRetryAt = recovery.value(QStringLiteral("next_retry_at")).toString();
        const QJsonObject validation = object.value(QStringLiteral("validation")).toObject();
        role.validationState = validation.value(QStringLiteral("state")).toString();
        role.validationReason = validation.value(QStringLiteral("reason")).toString();
        role.server = role.sessionEndpoint.isEmpty() ? QStringLiteral("—") : role.sessionEndpoint;
        for (const QJsonValue &action : object.value(QStringLiteral("available_actions")).toArray())
            role.availableActions.append(action.toString());
        mapped.append(role);
    }
    // Membership is authoritative when published by the core. Previously this
    // map knew only edits issued in the current UI process, so on a fresh
    // launch every role incorrectly appeared in every selected profile.
    QHash<QString, QStringList> snapshotMembership;
    bool hasMembership = false;
    for (const QJsonValue &value : snapshot.value(QStringLiteral("roles")).toArray()) {
        const QJsonObject role = value.toObject();
        if (!role.value(QStringLiteral("profiles")).isArray()) continue;
        hasMembership = true;
        QStringList memberships;
        for (const QJsonValue &profile : role.value(QStringLiteral("profiles")).toArray())
            memberships.append(profile.toString());
        snapshotMembership.insert(role.value(QStringLiteral("label")).toString(), memberships);
    }
    if (hasMembership)
        m_roleProfiles = snapshotMembership;

    m_roleLabels.clear();
    for (const RoleSnapshot &entry : mapped)
        m_roleLabels.append(entry.label);

    // Rebuild the visible model only when the rendered content changed.
    const QString signature = snapshotSignature(std::move(mapped));
    if (signature != m_roleSignature) {
        m_roleSignature = signature;
        m_roles.setRoles(std::move(mapped));
        uiLog(QStringLiteral("model rebuild roles=%1").arg(m_roles.rowCount()));
    }
}
