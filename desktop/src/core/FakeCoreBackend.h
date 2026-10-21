#pragma once

#include "core/CoreBackend.h"
#include "models/RoleListModel.h"

class FakeCoreBackend final : public CoreBackend
{
    Q_OBJECT

public:
    explicit FakeCoreBackend(QObject *parent = nullptr);

    QString backendKind() const override { return QStringLiteral("fake"); }
    QString platformName() const override;
    QString coreState() const override { return QStringLiteral("Ready"); }
    QString activeProfile() const override { return m_activeProfile; }
    QStringList profiles() const override { return m_profiles; }
    QString underlaySummary() const override { return m_underlaySummary; }
    QVariantMap underlay() const override;
    QString lastCommandError() const override { return {}; }
    QString lastCommandMessage() const override { return m_commandMessage; }
    QString aggregateState() const override { return m_aggregateState; }
    QString aggregateStateText() const override;
    QString aggregateActionText() const override;
    qulonglong revision() const override { return m_revision; }
    bool leshySupported() const override;
    bool simulated() const override { return true; }
    RoleListModel *roles() override { return &m_roles; }

    static constexpr int fixtureCount = 10;

    void toggleAll() override;
    void connectAll() override;
    void disconnectAll() override;
    void roleAction(int row) override;
    void roleActionByName(int row, const QString &action) override;
    void retryRole(int row) override { roleAction(row); }
    void validateRole(int row) override { roleAction(row); }
    void rediscoverEndpoints(int row) override { Q_UNUSED(row); bumpRevision(); }
    void nextDemoScenario() override;
    void setActiveProfile(const QString &name) override;
    void createTunnel(const QVariantMap &spec) override;
    void createProfile(const QString &name) override;
    void setTunnelProfiles(const QString &name, const QStringList &profiles) override;
    void importTunnel(const QString &text) override;
    void removeTunnel(const QString &name) override;
    QVariantMap routingLists() const override;
    QVariantList routingZones() const override;
    QString bypassMode() const override { return m_bypassMode; }
    void setBypassMode(const QString &mode) override;
    void refreshRoutingLists() override { bumpRevision(); }
    void saveRoutingList(const QString &name, const QString &content) override;
    void setLeshyZone(const QString &role, const QString &zone) override;
    QStringList profilesForRole(const QString &name) const override { return m_roleProfiles.value(name); }

private:
    void applyScenario(int scenario);
    void finishConnectAll();
    void finishDisconnectAll();
    void bumpRevision();
    bool anyRoleActive() const;

    RoleListModel m_roles;
    QString m_activeProfile = QStringLiteral("home");
    QStringList m_profiles = {QStringLiteral("home"), QStringLiteral("work")};
    QString m_underlaySummary = QStringLiteral("Wi-Fi · wlan0 · 192.168.1.52");
    QString m_commandMessage;
    QHash<QString, QStringList> m_roleProfiles;
    QString m_bypassMode = QStringLiteral("direct");
    QVariantMap m_routingLists{{QStringLiteral("primary"), QString()},
                               {QStringLiteral("secondary"), QString()},
                               {QStringLiteral("bypass"), QString()}};
    QString m_aggregateState = QStringLiteral("Stopped");
    qulonglong m_revision = 1;
    int m_scenario = 0;
};
