#pragma once

#include "core/CoreBackend.h"

#include <QByteArray>
#include <QJsonObject>
#include "core/CoreSocket.h"

class RealCoreClient final : public CoreBackend
{
    Q_OBJECT

public:
    explicit RealCoreClient(QObject *parent = nullptr);
    RealCoreClient(const QString &socketPath, QObject *parent);
    ~RealCoreClient() override;

    static QString defaultSocketPath();
    static bool isSocketAvailable(const QString &socketPath, int timeoutMs = 100);

    QString backendKind() const override;
    QString platformName() const override;
    QString coreState() const override;
    QString activeProfile() const override;
    QStringList profiles() const override;
    QString underlaySummary() const override;
    QVariantMap underlay() const override;
    QString lastCommandError() const override;
    QString lastCommandMessage() const override;
    QString aggregateState() const override;
    QString aggregateStateText() const override;
    QString aggregateActionText() const override;
    qulonglong revision() const override;
    bool leshySupported() const override;
    bool simulated() const override { return false; }
    RoleListModel *roles() override;

    void toggleAll() override;
    void connectAll() override;
    void disconnectAll() override;
    void roleAction(int row) override;
    void roleActionByName(int row, const QString &action) override;
    void retryRole(int row) override;
    void validateRole(int row) override;
    void rediscoverEndpoints(int row) override;
    void nextDemoScenario() override;
    void setActiveProfile(const QString &name) override;
    void createTunnel(const QVariantMap &spec) override;
    void createProfile(const QString &name) override;
    void setTunnelProfiles(const QString &name, const QStringList &profiles) override;
    void importTunnel(const QString &text) override;
    void removeTunnel(const QString &name) override;
    QStringList profilesForRole(const QString &name) const override;

private:
    void connectToCore();
    void sendHandshake();
    void sendGetSnapshot(const QString &id = QStringLiteral("getsnapshot"));
    void sendSubscribe();
    void sendCommand(const QString &method, const QJsonObject &params = {});
    void sendMessage(const QJsonObject &message);
    void processIncomingMessage();
    void handleSnapshot(const QJsonObject &snapshot, bool streamed, const QString &requestId);
    void updateModelFromSnapshot(const QJsonObject &snapshot);
    void setDisconnected();

    CoreSocket *socket;
    RoleListModel m_roles;
    QString m_socketPath;
    QString m_platformName;
    QString m_coreState = QStringLiteral("Disconnected");
    QString m_activeProfile;
    QStringList m_profiles;
    QString m_underlaySummary;
    QVariantMap m_underlay;
    QString m_lastCommandError;
    QString m_commandMessage;
    QString m_roleSignature;
    QHash<QString, QStringList> m_roleProfiles;
    QString m_aggregateState = QStringLiteral("Stopped");
    qulonglong m_revision = 0;
    bool m_leshySupported = false;
    bool m_resyncPending = false;
    int m_reconnectAttempt = 0;
    QByteArray m_buffer;
};
