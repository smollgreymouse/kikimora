#pragma once

#include "core/CoreBackend.h"
#include "models/RoleListModel.h"

// Schema/state simulator for the QML layer. It publishes exactly the shapes
// the real core snapshot uses (U2), including the fail-closed safety policy,
// so a QML page cannot tell the backends apart by their contract. It is a
// test/demo device, not a Windows product fallback.
class FakeCoreBackend final : public CoreBackend
{
    Q_OBJECT

public:
    explicit FakeCoreBackend(QObject *parent = nullptr);

    QString backendKind() const override { return QStringLiteral("fake"); }
    QString platformName() const override;
    QString coreState() const override { return m_coreState; }
    QString activeProfile() const override { return m_activeProfile; }
    QStringList profiles() const override { return m_profiles; }
    QString underlaySummary() const override { return m_underlaySummary; }
    QVariantMap underlay() const override;
    QString lastCommandError() const override { return m_lastCommandError; }
    QString lastCommandMessage() const override { return m_commandMessage; }
    QString aggregateState() const override { return m_aggregateState; }
    QString aggregateStateText() const override;
    QString aggregateActionText() const override;
    qulonglong revision() const override { return m_revision; }
    bool leshySupported() const override;
    bool simulated() const override { return true; }
    RoleListModel *roles() override { return &m_roles; }

    static constexpr int fixtureCount = 11;

    void toggleAll() override;
    void connectAll() override;
    void disconnectAll() override;
    void roleAction(int row) override;
    void roleActionByName(int row, const QString &action) override;
    void retryRole(int row) override { roleAction(row); }
    void validateRole(int row) override { roleAction(row); }
    void rediscoverEndpoints(int row) override { Q_UNUSED(row); bumpRevision(); }
    void setRoleIpv6(int row, bool enabled) override;
    void nextDemoScenario() override;
    void setActiveProfile(const QString &name) override;
    void createTunnel(const QVariantMap &spec) override;
    void createProfile(const QString &name, const QStringList &roles) override;
    void setTunnelProfiles(const QString &name, const QStringList &profiles) override;
    void importTunnel(const QString &text) override;
    void removeTunnel(const QString &name) override;
    QVariantMap routingLists() const override;
    QVariantList routingZones() const override;
    QString bypassMode() const override { return m_bypassMode; }
    QStringList roleLabels() const override;
    QVariantMap observers() const override;
    QVariantMap diagnostics() const override;
    void refreshDiagnostics() override { bumpRevision(); }
    void setDiagnosticsCapture(bool enabled) override;
    bool m_captureEnabled = false;
    void setBypassMode(const QString &mode) override;
    void refreshRoutingLists() override { bumpRevision(); }
    void saveRoutingList(const QString &name, const QString &content) override;
    QStringList profilesForRole(const QString &name) const override { return m_roleProfiles.value(name); }

    // --- Safety truth and policy, same rules as RealCoreClient (U1/U2) ---
    bool startupReady() const override { return m_startupReady; }
    bool managedMode() const override { return m_managedMode; }
    qulonglong modeGeneration() const override { return m_modeGeneration; }
    QString activationIdFingerprint() const override { return m_activationIdFingerprint; }
    QString modeTransition() const override { return m_modeTransition; }
    QString modeTransitionIdFingerprint() const override { return m_modeTransitionIdFingerprint; }
    bool routeAuthorityReady() const override { return m_routeAuthorityReady; }
    QString routeAuthorityError() const override { return m_routeAuthorityError; }
    bool commandChannelReady() const override;
    bool apiCompatible() const override { return m_apiCompatible; }
    QStringList coreCapabilities() const override { return {QStringLiteral("Subscribe")}; }
    bool safeToMutate() const override;
    bool safeToDisconnect() const override { return commandChannelReady(); }
    bool diagnosticsAvailable() const override { return m_apiCompatible; }
    bool diagnosticsMutable() const override;

    // Scenario controls for the QML test layer: the fake is the only place
    // where safety states can be produced deterministically without a
    // privileged managed core.
    void setCoreState(const QString &state) { m_coreState = state; bumpRevision(); }
    void setApiCompatible(bool compatible);
    void setStartupReady(bool ready);
    void setManagedAuthority(bool managed, bool authorityReady);
    void setModeTransition(const QString &phase, const QString &fingerprint = {});
    void setRouteAuthorityError(const QString &error);
    // Selected-route counters for one role, as the managed core publishes them.
    void setRoleSelectedRoutes(int row, const QVariantMap &stats);

private:
    void applyScenario(int scenario);
    void finishConnectAll();
    void finishDisconnectAll();
    void bumpRevision();
    bool anyRoleActive() const;

    RoleListModel m_roles;
    QString m_coreState = QStringLiteral("Ready");
    QString m_activeProfile = QStringLiteral("default");
    // The real core always lists "default" first in the snapshot profiles; the
    // fake mirrors that contract so the switcher popup matches production.
    QStringList m_profiles = {QStringLiteral("default"), QStringLiteral("home"), QStringLiteral("work")};
    QString m_underlaySummary = QStringLiteral("Wi-Fi · wlan0 · 192.168.1.52");
    QString m_commandMessage;
    QString m_lastCommandError;
    QHash<QString, QStringList> m_roleProfiles;
    QString m_bypassMode = QStringLiteral("direct");
    QVariantMap m_routingLists{{QStringLiteral("primary"), QString()},
                               {QStringLiteral("secondary"), QString()},
                               {QStringLiteral("bypass"), QString()}};
    QString m_aggregateState = QStringLiteral("Stopped");
    qulonglong m_revision = 1;
    int m_scenario = 0;
    // Safety truth mirrored from the real snapshot contract; the defaults
    // describe a healthy local-mode core that just answered.
    bool m_apiCompatible = true;
    bool m_authoritativeSnapshot = true;
    bool m_startupReady = true;
    bool m_managedMode = false;
    bool m_routeAuthorityReady = false;
    qulonglong m_modeGeneration = 0;
    QString m_activationIdFingerprint;
    QString m_modeTransition = QStringLiteral("none");
    QString m_modeTransitionIdFingerprint;
    QString m_routeAuthorityError;
};
