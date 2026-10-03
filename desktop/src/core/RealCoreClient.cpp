#include "RealCoreClient.h"

#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QSysInfo>
#include <QTimer>
#include <QUuid>

#include <iterator>
#include <utility>

namespace {
constexpr int apiVersion = 2;

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
    , socket(new QLocalSocket(this))
    , m_socketPath(socketPath)
    , m_platformName(QSysInfo::productType())
{
    connect(socket, &QLocalSocket::connected, this, &RealCoreClient::sendHandshake);
    connect(socket, &QLocalSocket::readyRead, this, &RealCoreClient::processIncomingMessage);
    connect(socket, &QLocalSocket::disconnected, this, &RealCoreClient::setDisconnected);
    connect(socket, &QLocalSocket::errorOccurred, this, [this]() {
        m_coreState = QStringLiteral("Unavailable");
        emit snapshotChanged();
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
    return QStringLiteral("/run/kikimora/core.sock");
}

bool RealCoreClient::isSocketAvailable(const QString &socketPath, int timeoutMs)
{
    QLocalSocket probe;
    probe.connectToServer(socketPath);
    const bool connected = probe.waitForConnected(timeoutMs);
    probe.abort();
    return connected;
}

QString RealCoreClient::backendKind() const { return QStringLiteral("real"); }
QString RealCoreClient::platformName() const { return m_platformName; }
QString RealCoreClient::coreState() const { return m_coreState; }
QString RealCoreClient::activeProfile() const { return m_activeProfile; }
QString RealCoreClient::underlaySummary() const { return m_underlaySummary; }
QVariantMap RealCoreClient::underlay() const { return m_underlay; }
QString RealCoreClient::lastCommandError() const { return m_lastCommandError; }
QString RealCoreClient::aggregateState() const { return m_aggregateState; }

QString RealCoreClient::aggregateStateText() const
{
    if (m_aggregateState == QStringLiteral("Ready")) return QStringLiteral("Protected");
    if (m_aggregateState == QStringLiteral("Connecting")) return QStringLiteral("Connecting all tunnels…");
    if (m_aggregateState == QStringLiteral("WaitingForUnderlay")) return QStringLiteral("Waiting for physical network");
    if (m_aggregateState == QStringLiteral("Recovering")) return QStringLiteral("Partially protected · recovering");
    if (m_aggregateState == QStringLiteral("Failed") || m_aggregateState == QStringLiteral("Degraded"))
        return QStringLiteral("Protection degraded");
    return QStringLiteral("Not connected");
}

QString RealCoreClient::aggregateActionText() const
{
    return m_aggregateState == QStringLiteral("Ready") ||
                   m_aggregateState == QStringLiteral("Connecting") ||
                   m_aggregateState == QStringLiteral("Degraded")
               ? QStringLiteral("DISCONNECT")
               : QStringLiteral("CONNECT");
}

qulonglong RealCoreClient::revision() const { return m_revision; }
bool RealCoreClient::leshySupported() const { return m_leshySupported; }
RoleListModel *RealCoreClient::roles() { return &m_roles; }

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
    const QString method = role.availableActions.contains(QStringLiteral("retry"))
                               ? QStringLiteral("RetryRole")
                               : (role.availableActions.contains(QStringLiteral("disconnect"))
                                      ? QStringLiteral("DisconnectRole")
                                      : QStringLiteral("ConnectRole"));
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

void RealCoreClient::rediscoverEndpoints(int row)
{
    const RoleSnapshot role = m_roles.roleAt(row);
    if (!role.id.isEmpty()) sendCommand(QStringLiteral("RediscoverEndpoints"), {{QStringLiteral("role"), role.id}});
}

void RealCoreClient::nextDemoScenario() {}

void RealCoreClient::connectToCore()
{
    if (socket->state() != QLocalSocket::UnconnectedState) return;
    m_coreState = QStringLiteral("Connecting");
    emit snapshotChanged();
    socket->connectToServer(m_socketPath);
}

void RealCoreClient::setDisconnected()
{
    m_coreState = QStringLiteral("Unavailable");
    emit snapshotChanged();
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

void RealCoreClient::sendCommand(const QString &method, const QJsonObject &params)
{
    QJsonObject request{{QStringLiteral("version"), apiVersion},
                        {QStringLiteral("id"), QUuid::createUuid().toString(QUuid::WithoutBraces)},
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
        const int responseVersion = object.value(QStringLiteral("version")).toInt();
        if (responseVersion != 1 && responseVersion != apiVersion) continue;
        const QString id = object.value(QStringLiteral("id")).toString();
        if (!object.value(QStringLiteral("ok")).toBool()) {
            const QJsonObject detail = object.value(QStringLiteral("error_detail")).toObject();
            const QJsonObject error = object.value(QStringLiteral("error")).toObject();
            m_lastCommandError = detail.value(QStringLiteral("message")).toString(
                error.value(QStringLiteral("message")).toString(object.value(QStringLiteral("error")).toString()));
            emit snapshotChanged();
            continue;
        }
        if (id == QStringLiteral("handshake")) {
            m_reconnectAttempt = 0;
            m_coreState = QStringLiteral("Connected");
            emit snapshotChanged();
            sendGetSnapshot();
            sendSubscribe();
        }
        if (object.value(QStringLiteral("snapshot")).isObject())
            handleSnapshot(object.value(QStringLiteral("snapshot")).toObject(), id == QStringLiteral("subscribe"), id);
    }
}

void RealCoreClient::handleSnapshot(const QJsonObject &snapshot, bool streamed, const QString &requestId)
{
    const qulonglong nextRevision = snapshot.value(QStringLiteral("revision")).toVariant().toULongLong();
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
        role.desiredEnabled = object.value(QStringLiteral("desired_enabled")).toBool(false) || object.value(QStringLiteral("desired_state")).toString() == QStringLiteral("enabled");
        role.operation = object.value(QStringLiteral("operation")).toVariant().toULongLong();
        role.validatedUnderlayEpoch = object.value(QStringLiteral("validated_underlay_epoch")).toVariant().toULongLong();
        const QJsonObject endpoint = object.value(QStringLiteral("endpoint")).toObject();
        role.endpointState = endpoint.value(QStringLiteral("state")).toString(role.endpointState);
        for (const QJsonValue &value : endpoint.value(QStringLiteral("configured")).toArray()) role.configuredEndpoints.append(value.toString());
        for (const QJsonValue &value : endpoint.value(QStringLiteral("live")).toArray()) role.liveEndpoints.append(value.toString());
        const QJsonObject publication = object.value(QStringLiteral("publication")).toObject();
        role.leshyPublished = publication.value(QStringLiteral("published")).toBool(role.leshyPublished);
        role.leshyZone = publication.value(QStringLiteral("zone")).toString();
        const QJsonObject parking = object.value(QStringLiteral("parking")).toObject();
        role.parkingActive = parking.value(QStringLiteral("active")).toBool(false);
        role.parkedRoutes = parking.value(QStringLiteral("count")).toInt(role.parkedRoutes);
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
    m_roles.setRoles(std::move(mapped));
}
