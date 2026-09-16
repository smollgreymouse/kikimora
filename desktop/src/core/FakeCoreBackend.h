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
    QString underlaySummary() const override { return m_underlaySummary; }
    QString aggregateState() const override { return m_aggregateState; }
    QString aggregateStateText() const override;
    QString aggregateActionText() const override;
    qulonglong revision() const override { return m_revision; }
    bool leshySupported() const override;
    RoleListModel *roles() override { return &m_roles; }

    void toggleAll() override;
    void connectAll() override;
    void disconnectAll() override;
    void roleAction(int row) override;
    void nextDemoScenario() override;

private:
    void applyScenario(int scenario);
    void finishConnectAll();
    void finishDisconnectAll();
    void bumpRevision();
    bool anyRoleActive() const;

    RoleListModel m_roles;
    QString m_activeProfile = QStringLiteral("home");
    QString m_underlaySummary = QStringLiteral("Wi-Fi · wlan0 · 192.168.1.52");
    QString m_aggregateState = QStringLiteral("Stopped");
    qulonglong m_revision = 1;
    int m_scenario = 0;
};
