#include "core/FakeCoreBackend.h"

#include <QRegularExpression>
#include <QSysInfo>
#include <QTimer>

namespace {
// Core-reported capability list for a role in a given state. The UI must hide or
// disable actions based on this list instead of guessing from the state string.
QStringList actionsForState(const QString &state)
{
    if (state == QStringLiteral("Stopped"))
        return {QStringLiteral("connect")};
    if (state == QStringLiteral("Unsupported"))
        return {};
    if (state == QStringLiteral("Connecting") || state == QStringLiteral("Disconnecting"))
        return {};
    // Ready / Recovering / Failed / WaitingForUnderlay all allow recovery.
    if (state == QStringLiteral("Ready"))
        return {QStringLiteral("disconnect"), QStringLiteral("retry")};
    return {QStringLiteral("retry"), QStringLiteral("disconnect")};
}

RoleSnapshot withActions(RoleSnapshot role)
{
    role.availableActions = actionsForState(role.state);
    const bool ready = role.state == QStringLiteral("Ready");
    role.routeReady = ready;
    role.interfaceIndex = 5;
    role.interfaceMtu = 1420;
    // A role is its tunnel plus its own list; the first two roles mirror the shipped zones.

    role.leshyPublished = role.leshyPublished && ready;
    // A role is its tunnel plus its own list, and the list is named after the role.
    role.leshyZone = role.label;
    role.sessionConnected = ready;
    if (role.sessionEndpoint.isEmpty())
        role.sessionEndpoint = role.server;
    if (role.configuredEndpoints.isEmpty())
        role.configuredEndpoints = {QStringLiteral("203.0.113.10"), QStringLiteral("203.0.113.11")};
    if (role.liveEndpoints.isEmpty() && ready)
        role.liveEndpoints = {QStringLiteral("203.0.113.10")};
    return role;
}

RoleSnapshot primaryRole()
{
    return withActions({
        .id = QStringLiteral("primary"),
        .label = QStringLiteral("Primary"),
        .protocol = QStringLiteral("AmneziaWG"),
        .server = QStringLiteral("NL · Amsterdam"),
        .state = QStringLiteral("Stopped"),
        .stateText = QStringLiteral("Stopped"),
        .interfaceName = QStringLiteral("amn0"),
        .underlay = QStringLiteral("wlan0 via 192.168.1.1"),
        .endpointState = QStringLiteral("ready"),
        .leshyPublished = false,
        .parkedRoutes = 0,
        .lastRecovery = QStringLiteral("—"),
        .lastError = QStringLiteral("—"),
        .lastHandshakeAgeMs = -1,
        .resolvedEndpoints = {QStringLiteral("203.0.113.10:51820")},
        .endpointLastError = QString(),
        .appliedUnderlayEpoch = 42,
        .parkedPrefixes = {},
        .pid = 4242,
        .generation = 7
    });
}

RoleSnapshot secondaryRole()
{
    return withActions({
        .id = QStringLiteral("secondary"),
        .label = QStringLiteral("Secondary"),
        .protocol = QStringLiteral("Xray"),
        .server = QStringLiteral("DE · Frankfurt"),
        .state = QStringLiteral("Stopped"),
        .stateText = QStringLiteral("Stopped"),
        .interfaceName = QStringLiteral("tun0"),
        .underlay = QStringLiteral("wlan0 via 192.168.1.1"),
        .endpointState = QStringLiteral("ready"),
        .leshyPublished = false,
        .parkedRoutes = 0,
        .lastRecovery = QStringLiteral("—"),
        .lastError = QStringLiteral("—")
    });
}
}

FakeCoreBackend::FakeCoreBackend(QObject *parent)
    : CoreBackend(parent)
{
    m_roles.setRoles({primaryRole(), secondaryRole()});
    // The fake publishes memberships like the core snapshot does; without them every
    // role would look unknown and the profile filter would show it everywhere.
    for (int row = 0; row < m_roles.rowCount(); ++row)
        m_roleProfiles[m_roles.roleAt(row).label] = m_profiles;
}

QString FakeCoreBackend::platformName() const
{
#if defined(Q_OS_WIN)
    return QStringLiteral("windows");
#elif defined(Q_OS_MACOS)
    return QStringLiteral("macos");
#else
    return QStringLiteral("linux");
#endif
}

QVariantMap FakeCoreBackend::underlay() const
{
    return {{QStringLiteral("available"), m_underlaySummary != QStringLiteral("Physical network unavailable")},
            {QStringLiteral("summary"), m_underlaySummary},
            // The real client publishes the live epoch (RealCoreClient parses
            // snapshot.underlay.epoch); the fake mirrors it so the role drawer
            // can compare validated vs current epochs.
            {QStringLiteral("epoch"), 42}};
}

QVariantMap FakeCoreBackend::observers() const
{
    return {{QStringLiteral("netlink_healthy"), true},
            {QStringLiteral("underlay_converger_healthy"), true},
            {QStringLiteral("sleep_healthy"), true},
            {QStringLiteral("networkmanager_healthy"), true}};
}

QVariantMap FakeCoreBackend::diagnostics() const
{
    return {{QStringLiteral("core_version"), QStringLiteral("fake-1.0.0")},
            {QStringLiteral("protocol_version"), QStringLiteral("1")},
            {QStringLiteral("platform"), platformName()},
            {QStringLiteral("socket_path"), QStringLiteral("fake://core.sock")},
            {QStringLiteral("managed_interfaces"), QVariantMap{{QStringLiteral("kk-primary0"), QStringLiteral("kernel")}}},
            {QStringLiteral("desired_state_status"), QStringLiteral("restored")}};
}

bool FakeCoreBackend::leshySupported() const
{
#if defined(Q_OS_WIN)
    return false;
#else
    return true;
#endif
}

QString FakeCoreBackend::aggregateStateText() const
{
    if (m_aggregateState == QStringLiteral("Ready")) return QStringLiteral("Protected");
    if (m_aggregateState == QStringLiteral("Connecting")) return QStringLiteral("Connecting all tunnels…");
    if (m_aggregateState == QStringLiteral("Disconnecting")) return QStringLiteral("Disconnecting…");
    if (m_aggregateState == QStringLiteral("Recovering")) return QStringLiteral("Partially protected · recovering");
    if (m_aggregateState == QStringLiteral("PartiallyReady")) return QStringLiteral("Partially protected");
    if (m_aggregateState == QStringLiteral("WaitingForUnderlay")) return QStringLiteral("Waiting for physical network");
    if (m_aggregateState == QStringLiteral("Failed")) return QStringLiteral("Protection degraded");
    return QStringLiteral("Not connected");
}

QString FakeCoreBackend::aggregateActionText() const
{
    return anyRoleActive() ? QStringLiteral("DISCONNECT") : QStringLiteral("CONNECT");
}

bool FakeCoreBackend::anyRoleActive() const
{
    for (int i = 0; i < m_roles.rowCount(); ++i) {
        const auto state = m_roles.roleAt(i).state;
        if (state != QStringLiteral("Stopped") && state != QStringLiteral("Unsupported"))
            return true;
    }
    return false;
}

void FakeCoreBackend::toggleAll()
{
    anyRoleActive() ? disconnectAll() : connectAll();
}

void FakeCoreBackend::connectAll()
{
    m_scenario = 0;
    for (int i = 0; i < m_roles.rowCount(); ++i) {
        auto role = m_roles.roleAt(i);
        role.state = QStringLiteral("Connecting");
        role.stateText = QStringLiteral("Connecting");
        role.lastError = QStringLiteral("—");
        m_roles.updateRole(i, withActions(role));
    }
    m_aggregateState = QStringLiteral("Connecting");
    bumpRevision();
    QTimer::singleShot(850, this, &FakeCoreBackend::finishConnectAll);
}

void FakeCoreBackend::finishConnectAll()
{
    for (int i = 0; i < m_roles.rowCount(); ++i) {
        auto role = m_roles.roleAt(i);
        role.state = QStringLiteral("Ready");
        role.stateText = QStringLiteral("Ready");
        role.leshyPublished = leshySupported();
        role.parkedRoutes = 0;
        role.lastRecovery = QStringLiteral("Initial connect");
        m_roles.updateRole(i, withActions(role));
    }
    m_aggregateState = QStringLiteral("Ready");
    bumpRevision();
}

void FakeCoreBackend::disconnectAll()
{
    for (int i = 0; i < m_roles.rowCount(); ++i) {
        auto role = m_roles.roleAt(i);
        role.state = QStringLiteral("Disconnecting");
        role.stateText = QStringLiteral("Disconnecting");
        role.leshyPublished = false;
        m_roles.updateRole(i, withActions(role));
    }
    m_aggregateState = QStringLiteral("Disconnecting");
    bumpRevision();
    QTimer::singleShot(600, this, &FakeCoreBackend::finishDisconnectAll);
}

void FakeCoreBackend::finishDisconnectAll()
{
    for (int i = 0; i < m_roles.rowCount(); ++i) {
        auto role = m_roles.roleAt(i);
        role.state = QStringLiteral("Stopped");
        role.stateText = QStringLiteral("Stopped");
        role.leshyPublished = false;
        role.parkedRoutes = 0;
        m_roles.updateRole(i, withActions(role));
    }
    m_aggregateState = QStringLiteral("Stopped");
    bumpRevision();
}

void FakeCoreBackend::roleAction(int row)
{
    const auto role = m_roles.roleAt(row);
    if (role.id.isEmpty())
        return;
    roleActionByName(row, role.availableActions.isEmpty()
                              ? (role.state == QStringLiteral("Stopped") ? QStringLiteral("connect")
                                                                         : QStringLiteral("disconnect"))
                              : role.availableActions.first());
}

void FakeCoreBackend::roleActionByName(int row, const QString &action)
{
    auto role = m_roles.roleAt(row);
    if (role.id.isEmpty())
        return;

    bool changed = true;
    if (action == QStringLiteral("connect")) {
        role.state = QStringLiteral("Ready");
        role.stateText = QStringLiteral("Ready");
        role.leshyPublished = leshySupported();
        role.parkedRoutes = 0;
        role.lastRecovery = QStringLiteral("Manual per-role connect");
        role.lastError = QStringLiteral("—");
    } else if (action == QStringLiteral("disconnect")) {
        role.state = QStringLiteral("Stopped");
        role.stateText = QStringLiteral("Stopped");
        role.leshyPublished = false;
        role.parkedRoutes = 0;
        role.lastRecovery = QStringLiteral("Manual per-role disconnect");
    } else if (action == QStringLiteral("retry")) {
        role.state = QStringLiteral("Ready");
        role.stateText = QStringLiteral("Ready");
        role.leshyPublished = leshySupported();
        role.lastRecovery = QStringLiteral("Manual retry");
        role.lastError = QStringLiteral("—");
    } else if (action == QStringLiteral("validate")) {
        role.validationState = QStringLiteral("validated");
        role.validatedUnderlayEpoch = role.validatedUnderlayEpoch == 0 ? 1 : role.validatedUnderlayEpoch;
        role.lastRecovery = QStringLiteral("Manual validation");
    } else if (action == QStringLiteral("rediscover")) {
        role.endpointState = QStringLiteral("ready");
        role.lastRecovery = QStringLiteral("Manual endpoint rediscovery");
    } else {
        changed = false;
    }

    if (!changed)
        return;

    m_roles.updateRole(row, withActions(role));

    bool allReady = true;
    bool anyActive = false;
    for (int i = 0; i < m_roles.rowCount(); ++i) {
        const auto s = m_roles.roleAt(i).state;
        allReady = allReady && s == QStringLiteral("Ready");
        anyActive = anyActive || s != QStringLiteral("Stopped");
    }
    m_aggregateState = allReady ? QStringLiteral("Ready")
                                : (anyActive ? QStringLiteral("Recovering") : QStringLiteral("Stopped"));
    bumpRevision();
}

void FakeCoreBackend::nextDemoScenario()
{
    m_scenario = (m_scenario + 1) % fixtureCount;
    applyScenario(m_scenario);
}

void FakeCoreBackend::setActiveProfile(const QString &name)
{
    if (!m_profiles.contains(name) || m_activeProfile == name)
        return;
    m_activeProfile = name;
    bumpRevision();
}

// Eleven deterministic fixtures. Index 0 is the healthy/stopped baseline; the
// remainder cover the recovery, underlay-loss, per-role failure and partial
// protection states the core snapshot contract exposes. Every fixture is fully
// specified (no ad-hoc QML booleans) so tests can assert on each one.
void FakeCoreBackend::applyScenario(int scenario)
{
    auto p = primaryRole();
    auto s = secondaryRole();
    m_underlaySummary = QStringLiteral("Wi-Fi · wlan0 · 192.168.1.52");

    switch (scenario) {
    case 1:
        p.state = p.stateText = QStringLiteral("Ready");
        p.leshyPublished = leshySupported();
        p.lastRecovery = QStringLiteral("Validated on underlay epoch 42");
        s.state = s.stateText = QStringLiteral("Recovering");
        s.parkedRoutes = 14;
        s.lastRecovery = QStringLiteral("Transport restart after underlay change");
        m_aggregateState = QStringLiteral("Recovering");
        break;
    case 2:
        p.state = p.stateText = QStringLiteral("WaitingForUnderlay");
        p.endpointState = QStringLiteral("pending");
        p.parkedRoutes = 8;
        s.state = s.stateText = QStringLiteral("WaitingForUnderlay");
        s.endpointState = QStringLiteral("pending");
        s.parkedRoutes = 14;
        m_underlaySummary = QStringLiteral("Physical network unavailable");
        m_aggregateState = QStringLiteral("WaitingForUnderlay");
        break;
    case 3:
        p.state = p.stateText = QStringLiteral("Ready");
        p.leshyPublished = leshySupported();
        s.state = s.stateText = QStringLiteral("Failed");
        s.parkedRoutes = 14;
        s.lastError = QStringLiteral("Demo: transport validation failed");
        s.lastRecovery = QStringLiteral("Full role restart exhausted");
        m_aggregateState = QStringLiteral("Failed");
        break;
    case 4:
        p.state = p.stateText = QStringLiteral("Ready");
        p.leshyPublished = leshySupported();
        p.lastRecovery = QStringLiteral("Validated on underlay epoch 42");
        s.state = s.stateText = QStringLiteral("Ready");
        s.leshyPublished = leshySupported();
        s.lastRecovery = QStringLiteral("Validated on underlay epoch 42");
        m_aggregateState = QStringLiteral("Ready");
        break;
    case 5:
        p.state = p.stateText = QStringLiteral("Recovering");
        p.parkedRoutes = 6;
        p.lastRecovery = QStringLiteral("Transport restart after underlay change");
        s.state = s.stateText = QStringLiteral("Ready");
        s.leshyPublished = leshySupported();
        m_aggregateState = QStringLiteral("Recovering");
        break;
    case 6:
        p.state = p.stateText = QStringLiteral("Stopped");
        s.state = s.stateText = QStringLiteral("Ready");
        s.leshyPublished = leshySupported();
        s.lastRecovery = QStringLiteral("Validated on underlay epoch 42");
        m_aggregateState = QStringLiteral("Recovering");
        break;
    case 7:
        p.state = p.stateText = QStringLiteral("Connecting");
        s.state = s.stateText = QStringLiteral("Connecting");
        m_aggregateState = QStringLiteral("Connecting");
        break;
    case 8:
        p.state = p.stateText = QStringLiteral("Recovering");
        p.validationState = QStringLiteral("pending");
        p.parkedRoutes = 4;
        p.lastRecovery = QStringLiteral("Awaiting current-epoch validation");
        s.state = s.stateText = QStringLiteral("WaitingForUnderlay");
        s.parkedRoutes = 14;
        s.endpointState = QStringLiteral("pending");
        m_aggregateState = QStringLiteral("Recovering");
        break;
    case 9:
        p.state = p.stateText = QStringLiteral("Failed");
        p.parkedRoutes = 20;
        p.lastError = QStringLiteral("Demo: recovery attempts exhausted");
        p.lastRecovery = QStringLiteral("Full role restart exhausted");
        s.state = s.stateText = QStringLiteral("Stopped");
        m_aggregateState = QStringLiteral("Failed");
        break;
    case 10:
        // The core reports PartiallyReady when some desired roles are Ready
        // and the rest are neither recovering nor waiting (toad/internal/core/
        // snapshot.go). This is the state where the connect control used to
        // offer a dead CONNECT while two thirds of the tunnels were live.
        p.state = p.stateText = QStringLiteral("Ready");
        p.leshyPublished = leshySupported();
        p.lastRecovery = QStringLiteral("Validated on underlay epoch 42");
        s.state = s.stateText = QStringLiteral("Connecting");
        s.lastError = QStringLiteral("Demo: data phase stalled");
        m_aggregateState = QStringLiteral("PartiallyReady");
        break;
    default:
        m_aggregateState = QStringLiteral("Stopped");
        break;
    }

    m_roles.setRoles({withActions(p), withActions(s)});
    bumpRevision();
}

void FakeCoreBackend::bumpRevision()
{
    ++m_revision;
    emit snapshotChanged();
}

void FakeCoreBackend::createTunnel(const QVariantMap &spec)
{
	const QString name = spec.value(QStringLiteral("name")).toString().trimmed();
	const QString protocol = spec.value(QStringLiteral("protocol")).toString().trimmed();
	if (name.isEmpty() || protocol.isEmpty()) {
		m_commandMessage = QStringLiteral("tunnel name and protocol are required");
		bumpRevision();
		return;
	}

	RoleSnapshot role;
	role.id = name;
	role.label = name;
	role.protocol = protocol;
	role.server = spec.value(QStringLiteral("endpoint")).toString();
	role.state = QStringLiteral("Stopped");
	role.stateText = QStringLiteral("Stopped");
	m_roles.addRole(role);

	const QString profile = spec.value(QStringLiteral("profile")).toString().trimmed();
	if (!profile.isEmpty() && !m_profiles.contains(profile)) m_profiles.append(profile);
	m_commandMessage = QStringLiteral("created %1 (simulated)").arg(name);
	bumpRevision();
}

void FakeCoreBackend::setBypassMode(const QString &mode)
{
	m_bypassMode = mode.isEmpty() ? QStringLiteral("direct") : mode;
	m_commandMessage = QStringLiteral("bypass mode: %1 (simulated)").arg(m_bypassMode);
	bumpRevision();
}

QStringList FakeCoreBackend::roleLabels() const
{
	QStringList labels;
	for (int row = 0; row < m_roles.rowCount(); ++row)
		labels.append(m_roles.roleAt(row).label);
	return labels;
}

QVariantMap FakeCoreBackend::routingLists() const
{
	QVariantMap lists;
	for (int row = 0; row < m_roles.rowCount(); ++row)
		lists.insert(m_roles.roleAt(row).label, QString());
	lists.insert(QStringLiteral("bypass"), QString());
	for (auto it = m_routingLists.cbegin(); it != m_routingLists.cend(); ++it)
		lists.insert(it.key(), it.value());
	return lists;
}

QVariantList FakeCoreBackend::routingZones() const
{
	QVariantList zones;
	zones.append(QVariantMap{{QStringLiteral("name"), QStringLiteral("primary")},
	                          {QStringLiteral("mode"), QStringLiteral("inclusive")},
	                          {QStringLiteral("route_target"), QStringLiteral("/run/kikimora/leshy/vpn/awg.dev")}});
	zones.append(QVariantMap{{QStringLiteral("name"), QStringLiteral("secondary")},
	                          {QStringLiteral("mode"), QStringLiteral("inclusive")},
	                          {QStringLiteral("route_target"), QStringLiteral("/run/kikimora/leshy/vpn/xray.dev")}});
	return zones;
}

void FakeCoreBackend::saveRoutingList(const QString &name, const QString &content)
{
	QStringList kept;
	for (const QString &raw : content.split(QLatin1Char('\n'))) {
		const QString line = raw.trimmed();
		if (line.isEmpty() || line.startsWith(QLatin1Char('#'))) continue;
		kept.append(line.toLower());
	}
	m_routingLists[name] = kept.join(QLatin1Char('\n'));
	m_commandMessage = QStringLiteral("%1 list saved (simulated)").arg(name);
	bumpRevision();
}

void FakeCoreBackend::removeTunnel(const QString &name)
{
	QVector<RoleSnapshot> kept;
	for (int row = 0; row < m_roles.rowCount(); ++row) {
		const RoleSnapshot role = m_roles.roleAt(row);
		if (role.label != name) kept.append(role);
	}
	m_roles.setRoles(kept);
	m_commandMessage = QStringLiteral("removed %1 (simulated)").arg(name);
	bumpRevision();
}

void FakeCoreBackend::importTunnel(const QString &text)
{
	const TunnelImport parsed = ParseTunnelImport(text);
	if (parsed.spec.isEmpty()) {
		m_commandMessage = parsed.error;
		bumpRevision();
		return;
	}
	createTunnel(parsed.spec);
}

void FakeCoreBackend::setTunnelProfiles(const QString &name, const QStringList &profiles)
{
	QStringList membership;
	for (const QString &profile : profiles) {
		if (!profile.trimmed().isEmpty() && !membership.contains(profile.trimmed()))
			membership.append(profile.trimmed());
	}
	if (name.trimmed().isEmpty() || membership.isEmpty()) {
		m_commandMessage = QStringLiteral("tunnel name and at least one profile are required");
		bumpRevision();
		return;
	}
	m_roleProfiles[name.trimmed()] = membership;
	for (const QString &profile : membership) {
		if (!m_profiles.contains(profile)) m_profiles.append(profile);
	}
	m_commandMessage = QStringLiteral("%1 moved to [%2] (simulated)")
		.arg(name.trimmed(), membership.join(QStringLiteral(", ")));
	bumpRevision();
}

void FakeCoreBackend::createProfile(const QString &name, const QStringList &roles)
{
	const QString profile = name.trimmed();
	static const QRegularExpression validName(QStringLiteral("^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$"));
	auto reject = [this, &profile](const QString &reason) {
		m_commandMessage = reason;
		m_lastCommandError = reason;
		bumpRevision();
		emit profileCreationFinished(profile, false, reason);
	};
	if (profile.isEmpty()) {
		reject(QStringLiteral("profile name is required"));
		return;
	}
	if (!validName.match(profile).hasMatch()) {
		reject(QStringLiteral("invalid profile name %1").arg(profile));
		return;
	}
	if (m_profiles.contains(profile)) {
		reject(QStringLiteral("profile %1 already exists").arg(profile));
		return;
	}
	m_profiles.append(profile);
	// The new profile contains exactly the selected roles; every other profile keeps
	// its own set, so one role can serve several profiles.
	for (int row = 0; row < m_roles.rowCount(); ++row) {
		const QString label = m_roles.roleAt(row).label;
		QStringList membership = m_roleProfiles.value(label);
		if (roles.contains(label)) {
			if (!membership.contains(profile))
				membership.append(profile);
		} else {
			membership.removeAll(profile);
		}
		m_roleProfiles[label] = membership;
	}
	m_commandMessage = QStringLiteral("profile %1 added (simulated)").arg(profile);
	m_lastCommandError.clear();
	bumpRevision();
	emit profileCreationFinished(profile, true, m_commandMessage);
}
