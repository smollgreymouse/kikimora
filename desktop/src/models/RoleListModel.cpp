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
    case DesiredEnabledRole: return item.desiredEnabled;
    case OperationRole: return item.operation;
    case ValidatedUnderlayEpochRole: return item.validatedUnderlayEpoch;
    case RecoveryActionRole: return item.recoveryAction;
    case RecoveryReasonRole: return item.recoveryReason;
    case RecoveryAttemptRole: return item.recoveryAttempt;
    case NextRetryAtRole: return item.nextRetryAt;
    case ValidationStateRole: return item.validationState;
    case ValidationReasonRole: return item.validationReason;
    case ConfiguredEndpointsRole: return item.configuredEndpoints;
    case LiveEndpointsRole: return item.liveEndpoints;
    case LeshyZoneRole: return item.leshyZone;
    case ParkingActiveRole: return item.parkingActive;
    case AvailableActionsRole: return item.availableActions;
    case RouteReadyRole: return item.routeReady;
    case InterfaceIndexRole: return item.interfaceIndex;
    case InterfaceMtuRole: return item.interfaceMtu;
    case SessionConnectedRole: return item.sessionConnected;
    case SessionRxBytesRole: return item.sessionRxBytes;
    case SessionTxBytesRole: return item.sessionTxBytes;
    case SessionEndpointRole: return item.sessionEndpoint;
    case LastHandshakeAgeMsRole: return item.lastHandshakeAgeMs;
    case ResolvedEndpointsRole: return item.resolvedEndpoints;
    case EndpointLastErrorRole: return item.endpointLastError;
    case AppliedUnderlayEpochRole: return item.appliedUnderlayEpoch;
    case ParkedPrefixesRole: return item.parkedPrefixes;
    case PidRole: return item.pid;
    case GenerationRole: return item.generation;
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
        {LastErrorRole, "lastError"},
        {DesiredEnabledRole, "desiredEnabled"},
        {OperationRole, "operation"},
        {ValidatedUnderlayEpochRole, "validatedUnderlayEpoch"},
        {RecoveryActionRole, "recoveryAction"},
        {RecoveryReasonRole, "recoveryReason"},
        {RecoveryAttemptRole, "recoveryAttempt"},
        {NextRetryAtRole, "nextRetryAt"},
        {ValidationStateRole, "validationState"},
        {ValidationReasonRole, "validationReason"},
        {ConfiguredEndpointsRole, "configuredEndpoints"},
        {LiveEndpointsRole, "liveEndpoints"},
        {LeshyZoneRole, "leshyZone"},
        {ParkingActiveRole, "parkingActive"},
        {AvailableActionsRole, "availableActions"},
        {RouteReadyRole, "routeReady"},
        {InterfaceIndexRole, "interfaceIndex"},
        {InterfaceMtuRole, "interfaceMtu"},
        {SessionConnectedRole, "sessionConnected"},
        {SessionRxBytesRole, "sessionRxBytes"},
        {SessionTxBytesRole, "sessionTxBytes"},
        {SessionEndpointRole, "sessionEndpoint"},
        {LastHandshakeAgeMsRole, "lastHandshakeAgeMs"},
        {ResolvedEndpointsRole, "resolvedEndpoints"},
        {EndpointLastErrorRole, "endpointLastError"},
        {AppliedUnderlayEpochRole, "appliedUnderlayEpoch"},
        {ParkedPrefixesRole, "parkedPrefixes"},
        {PidRole, "pid"},
        {GenerationRole, "generation"}
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
        {"lastError", item.lastError},
        {"desiredEnabled", item.desiredEnabled},
        {"operation", item.operation},
        {"validatedUnderlayEpoch", item.validatedUnderlayEpoch},
        {"recoveryAction", item.recoveryAction},
        {"recoveryReason", item.recoveryReason},
        {"recoveryAttempt", item.recoveryAttempt},
        {"nextRetryAt", item.nextRetryAt},
        {"validationState", item.validationState},
        {"validationReason", item.validationReason},
        {"configuredEndpoints", item.configuredEndpoints},
        {"liveEndpoints", item.liveEndpoints},
        {"leshyZone", item.leshyZone},
        {"parkingActive", item.parkingActive},
        {"availableActions", item.availableActions},
        {"routeReady", item.routeReady},
        {"interfaceIndex", item.interfaceIndex},
        {"interfaceMtu", item.interfaceMtu},
        {"sessionConnected", item.sessionConnected},
        {"sessionRxBytes", item.sessionRxBytes},
        {"sessionTxBytes", item.sessionTxBytes},
        {"sessionEndpoint", item.sessionEndpoint},
        {"lastHandshakeAgeMs", item.lastHandshakeAgeMs},
        {"resolvedEndpoints", item.resolvedEndpoints},
        {"endpointLastError", item.endpointLastError},
        {"appliedUnderlayEpoch", item.appliedUnderlayEpoch},
        {"parkedPrefixes", item.parkedPrefixes},
        {"pid", item.pid},
        {"generation", item.generation}
    };
}

void RoleListModel::clear()
{
    if (m_roles.isEmpty())
        return;
    beginResetModel();
    m_roles.clear();
    endResetModel();
}

void RoleListModel::addRole(const RoleSnapshot &role)
{
    const int row = m_roles.size();
    beginInsertRows(QModelIndex(), row, row);
    m_roles.append(role);
    endInsertRows();
}
