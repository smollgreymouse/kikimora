#pragma once

#include <QAbstractListModel>
#include <QVariantMap>
#include <QVector>

struct RoleSnapshot
{
    QString id;
    QString label;
    QString protocol;
    QString server;
    QString state;
    QString stateText;
    QString interfaceName;
    QString underlay;
    QString endpointState;
    bool leshyPublished = false;
    int parkedRoutes = 0;
    QString lastRecovery;
    QString lastError;
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
        LastErrorRole
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

private:
    QVector<RoleSnapshot> m_roles;
};
