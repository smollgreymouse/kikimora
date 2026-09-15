#include "models/RoleListModel.h"

#include <utility>

RoleListModel::RoleListModel(QObject *parent) : QAbstractListModel(parent) {}

int RoleListModel::rowCount(const QModelIndex &parent) const
{
    return parent.isValid() ? 0 : m_roles.size();
}

QVariant RoleListModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= m_roles.size())
        return {};

    const auto &item = m_roles.at(index.row());
    switch (role) {
    case IdRole: return item.id;
    case LabelRole: return item.label;
    case ProtocolRole: return item.protocol;
    case ServerRole: return item.server;
    case StateRole: return item.state;
    case StateTextRole: return item.stateText;
    case InterfaceRole: return item.interfaceName;
    case UnderlayRole: return item.underlay;
    case EndpointStateRole: return item.endpointState;
    case LeshyPublishedRole: return item.leshyPublished;
    case ParkedRoutesRole: return item.parkedRoutes;
    case LastRecoveryRole: return item.lastRecovery;
    case LastErrorRole: return item.lastError;
    default: return {};
    }
}

QHash<int, QByteArray> RoleListModel::roleNames() const
{
    return {
        {IdRole, "roleId"},
        {LabelRole, "label"},
        {ProtocolRole, "protocol"},
        {ServerRole, "server"},
        {StateRole, "state"},
        {StateTextRole, "stateText"},
        {InterfaceRole, "interfaceName"},
        {UnderlayRole, "underlay"},
        {EndpointStateRole, "endpointState"},
        {LeshyPublishedRole, "leshyPublished"},
        {ParkedRoutesRole, "parkedRoutes"},
        {LastRecoveryRole, "lastRecovery"},
        {LastErrorRole, "lastError"}
    };
}

void RoleListModel::setRoles(QVector<RoleSnapshot> roles)
{
    beginResetModel();
    m_roles = std::move(roles);
    endResetModel();
}

void RoleListModel::updateRole(int row, const RoleSnapshot &role)
{
    if (row < 0 || row >= m_roles.size())
        return;
    m_roles[row] = role;
    const auto idx = index(row, 0);
    emit dataChanged(idx, idx);
}

RoleSnapshot RoleListModel::roleAt(int row) const
{
    if (row < 0 || row >= m_roles.size())
        return {};
    return m_roles.at(row);
}

QVariantMap RoleListModel::get(int row) const
{
    if (row < 0 || row >= m_roles.size())
        return {};

    const auto &item = m_roles.at(row);
    return {
        {"roleId", item.id},
        {"label", item.label},
        {"protocol", item.protocol},
        {"server", item.server},
        {"state", item.state},
        {"stateText", item.stateText},
        {"interfaceName", item.interfaceName},
        {"underlay", item.underlay},
        {"endpointState", item.endpointState},
        {"leshyPublished", item.leshyPublished},
        {"parkedRoutes", item.parkedRoutes},
        {"lastRecovery", item.lastRecovery},
        {"lastError", item.lastError}
    };
}
