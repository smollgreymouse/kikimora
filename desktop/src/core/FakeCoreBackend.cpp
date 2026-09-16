#include "core/FakeCoreBackend.h"

#include <QSysInfo>
#include <QTimer>

namespace {
RoleSnapshot primaryRole()
{
    return {
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
        .lastError = QStringLiteral("—")
    };
}

RoleSnapshot secondaryRole()
{
    return {
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
    };
}
}

FakeCoreBackend::FakeCoreBackend(QObject *parent)
    : CoreBackend(parent)
{
    m_roles.setRoles({primaryRole(), secondaryRole()});
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
        m_roles.updateRole(i, role);
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
        m_roles.updateRole(i, role);
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
        m_roles.updateRole(i, role);
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
        m_roles.updateRole(i, role);
    }
    m_aggregateState = QStringLiteral("Stopped");
    bumpRevision();
}

void FakeCoreBackend::roleAction(int row)
{
    auto role = m_roles.roleAt(row);
    if (role.id.isEmpty())
        return;

    const bool stop = role.state != QStringLiteral("Stopped");
    role.state = stop ? QStringLiteral("Stopped") : QStringLiteral("Ready");
    role.stateText = stop ? QStringLiteral("Stopped") : QStringLiteral("Ready");
    role.leshyPublished = !stop && leshySupported();
    role.parkedRoutes = 0;
    role.lastRecovery = stop ? QStringLiteral("Manual per-role disconnect") : QStringLiteral("Manual per-role connect");
    m_roles.updateRole(row, role);

    bool allReady = true;
    bool anyActive = false;
    for (int i = 0; i < m_roles.rowCount(); ++i) {
        const auto s = m_roles.roleAt(i).state;
        allReady = allReady && s == QStringLiteral("Ready");
        anyActive = anyActive || s != QStringLiteral("Stopped");
    }
    m_aggregateState = allReady ? QStringLiteral("Ready") : (anyActive ? QStringLiteral("Recovering") : QStringLiteral("Stopped"));
    bumpRevision();
}

void FakeCoreBackend::nextDemoScenario()
{
    m_scenario = (m_scenario + 1) % 4;
    applyScenario(m_scenario);
}

void FakeCoreBackend::applyScenario(int scenario)
{
    auto p = primaryRole();
    auto s = secondaryRole();

    switch (scenario) {
    case 1:
        p.state = p.stateText = QStringLiteral("Ready");
        p.leshyPublished = leshySupported();
        p.lastRecovery = QStringLiteral("Validated on underlay epoch 42");
        s.state = QStringLiteral("Recovering");
        s.stateText = QStringLiteral("Recovering");
        s.leshyPublished = false;
        s.parkedRoutes = 14;
        s.lastRecovery = QStringLiteral("Transport restart after underlay change");
        m_aggregateState = QStringLiteral("Recovering");
        break;
    case 2:
        p.state = QStringLiteral("WaitingForUnderlay");
        p.stateText = QStringLiteral("Waiting for network");
        p.endpointState = QStringLiteral("pending");
        p.parkedRoutes = 8;
        s.state = QStringLiteral("WaitingForUnderlay");
        s.stateText = QStringLiteral("Waiting for network");
        s.endpointState = QStringLiteral("pending");
        s.parkedRoutes = 14;
        m_underlaySummary = QStringLiteral("Physical network unavailable");
        m_aggregateState = QStringLiteral("WaitingForUnderlay");
        break;
    case 3:
        p.state = p.stateText = QStringLiteral("Ready");
        p.leshyPublished = leshySupported();
        s.state = QStringLiteral("Failed");
        s.stateText = QStringLiteral("Failed");
        s.parkedRoutes = 14;
        s.lastError = QStringLiteral("Demo: transport validation failed");
        s.lastRecovery = QStringLiteral("Full role restart exhausted");
        m_aggregateState = QStringLiteral("Failed");
        break;
    default:
        m_underlaySummary = QStringLiteral("Wi-Fi · wlan0 · 192.168.1.52");
        m_aggregateState = QStringLiteral("Stopped");
        break;
    }

    if (scenario != 2)
        m_underlaySummary = QStringLiteral("Wi-Fi · wlan0 · 192.168.1.52");

    m_roles.setRoles({p, s});
    bumpRevision();
}

void FakeCoreBackend::bumpRevision()
{
    ++m_revision;
    emit snapshotChanged();
}
