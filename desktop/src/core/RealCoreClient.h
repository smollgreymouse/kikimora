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
    void setRoleIpv6(int row, bool enabled) override;
    void nextDemoScenario() override;
    void setActiveProfile(const QString &name) override;
    void createTunnel(const QVariantMap &spec) override;
    void createProfile(const QString &name, const QStringList &roles) override;
    void setTunnelProfiles(const QString &name, const QStringList &profiles) override;
    void importTunnel(const QString &text) override;
    void removeTunnel(const QString &name) override;
    QStringList profilesForRole(const QString &name) const override;
    QVariantMap routingLists() const override;
    QVariantList routingZones() const override;
    QString bypassMode() const override;
    QStringList roleLabels() const override;
    void setBypassMode(const QString &mode) override;
    void refreshRoutingLists() override;
    void saveRoutingList(const QString &name, const QString &content) override;
    QVariantMap observers() const override { return m_observers; }
    QVariantMap diagnostics() const override { return m_diagnostics; }
    void refreshDiagnostics() override;
    void setDiagnosticsCapture(bool enabled) override;

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
    QStringList coreCapabilities() const override { return m_capabilities; }
    bool safeToMutate() const override;
    bool safeToDisconnect() const override { return commandChannelReady(); }
    bool diagnosticsAvailable() const override { return m_apiCompatible; }
    bool diagnosticsMutable() const override;

private:
    enum class CommandClass { Channel, Mutate, Disconnect, DiagnosticsRead, DiagnosticsMutate };

    // The safety class of every control method (roadmap §8.1). Unlisted
    // methods are handshake/lifecycle traffic and only need a live socket.
    static CommandClass commandClass(const QString &method);
    bool commandAllowed(CommandClass cls) const;
    // Set the command-scoped blocked reason without sending any IPC frame.
    void reportBlocked(const QString &method);
    void connectToCore();
    void sendHandshake();
    void sendGetSnapshot(const QString &id = QStringLiteral("getsnapshot"));
    void sendSubscribe();
    void sendCommand(const QString &method, const QJsonObject &params = {},
                     const QString &id = QString());
    void sendMessage(const QJsonObject &message);
    void processIncomingMessage();
    void handleSnapshot(const QJsonObject &snapshot, bool streamed, const QString &requestId);
    void updateModelFromSnapshot(const QJsonObject &snapshot);
    void setDisconnected();
    void scheduleReconnect();

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
    // Name of the profile a CreateProfile request is for: the reply is matched by
    // the request id and reported through profileCreationFinished.
    QString m_pendingProfileName;
    // Optimistic profile switch: the value shown while the core round-trip is
    // in flight, and the profile to restore when the core rejects the switch.
    bool m_pendingProfileSwitch = false;
    QString m_profileSwitchFallback;
    QString m_roleSignature;
    QVariantMap m_routingLists;
    QVariantList m_routingZones;
    QString m_bypassMode = QStringLiteral("direct");
    QStringList m_roleLabels;
    QVariantMap m_observers;
    QVariantMap m_diagnostics;
    // Set by saveRoutingList: the reply carries a message, the lists themselves come back
    // from a following GetRoutingLists.
    bool m_resyncRoutingLists = false;
    QHash<QString, QStringList> m_roleProfiles;
    QString m_aggregateState = QStringLiteral("Stopped");
    qulonglong m_revision = 0;
    bool m_leshySupported = false;
    bool m_resyncPending = false;
    int m_reconnectAttempt = 0;
    QByteArray m_buffer;
    // --- Safety truth mapped from the current core snapshot (U1) ---
    bool m_startupReady = false;
    bool m_managedMode = false;
    qulonglong m_modeGeneration = 0;
    QString m_activationIdFingerprint;
    QString m_modeTransition = QStringLiteral("none");
    QString m_modeTransitionIdFingerprint;
    bool m_routeAuthorityReady = false;
    QString m_routeAuthorityError;
    // apiCompatible means: this connection completed a compatible Handshake.
    // It never survives a disconnect.
    bool m_apiCompatible = false;
    QStringList m_capabilities;
    // True once an authoritative snapshot was received on the current
    // connection after the handshake.
    bool m_authoritativeSnapshot = false;
};
