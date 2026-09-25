#pragma once

#include <QAbstractListModel>
#include <QStringList>
#include <QVariantMap>
#include <QVector>

struct RoleSnapshot
{
    QString id;
    QString label;
    QString protocol;
    QString server;
    QString state;
    bool desiredEnabled = false;
    qulonglong operation = 0;
    qulonglong validatedUnderlayEpoch = 0;
    QString stateText;
    QString interfaceName;
    QString underlay;
    QString endpointState;
    bool leshyPublished = false;
    int parkedRoutes = 0;
    QString lastRecovery;
    QString lastError;
    QString recoveryAction;
    QString recoveryReason;
    int recoveryAttempt = 0;
    QString nextRetryAt;
    QString validationState;
    QString validationReason;
    QStringList configuredEndpoints;
    QStringList liveEndpoints;
    QString leshyZone;
    bool parkingActive = false;
    bool routeReady = false;
    int interfaceIndex = -1;
    int interfaceMtu = -1;
    bool sessionConnected = false;
    double sessionRxBytes = 0;
    double sessionTxBytes = 0;
    QString sessionEndpoint;
    QStringList availableActions;
};

class RoleListModel final : public QAbstractListModel
{
    Q_OBJECT

public:
    enum Roles {
        IdRole = Qt::UserRole + 1,
        LabelRole,
        ProtocolRole,
        ServerRole,
        StateRole,
        StateTextRole,
        InterfaceRole,
        UnderlayRole,
        EndpointStateRole,
        LeshyPublishedRole,
        ParkedRoutesRole,
        LastRecoveryRole,
        LastErrorRole,
        DesiredEnabledRole,
        OperationRole,
        ValidatedUnderlayEpochRole,
        RecoveryActionRole,
        RecoveryReasonRole,
        RecoveryAttemptRole,
        NextRetryAtRole,
        ValidationStateRole,
        ValidationReasonRole,
        ConfiguredEndpointsRole,
        LiveEndpointsRole,
        LeshyZoneRole,
        ParkingActiveRole,
        AvailableActionsRole
    };
    Q_ENUM(Roles)

    explicit RoleListModel(QObject *parent = nullptr);

    int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    QHash<int, QByteArray> roleNames() const override;

    void setRoles(QVector<RoleSnapshot> roles);
    void updateRole(int row, const RoleSnapshot &role);
    RoleSnapshot roleAt(int row) const;

    Q_INVOKABLE QVariantMap get(int row) const;

    void clear();
    void addRole(const RoleSnapshot &role);

private:
    QVector<RoleSnapshot> m_roles;
};
