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
    case LeshyIpv6Role: return item.leshyIpv6;
    case GenerationRole: return item.generation;
    case Ipv4ConfiguredRole: return item.ipv4Configured;
    case Ipv4ReadyRole: return item.ipv4Ready;
    case Ipv4ReasonRole: return item.ipv4Reason;
    case Ipv6ConfiguredRole: return item.ipv6Configured;
    case Ipv6ReadyRole: return item.ipv6Ready;
    case Ipv6ReasonRole: return item.ipv6Reason;
    case SelectedRoutesRole: return item.selectedRoutes;
    default: return {};
    }
}

QHash<int, QByteArray> RoleListModel::roleNames() const
{
    QHash<int, QByteArray> names;
    names.insert(IdRole, "roleId");
    names.insert(LabelRole, "label");
    names.insert(ProtocolRole, "protocol");
    names.insert(ServerRole, "server");
    names.insert(StateRole, "state");
    names.insert(StateTextRole, "stateText");
    names.insert(InterfaceRole, "interfaceName");
    names.insert(UnderlayRole, "underlay");
    names.insert(EndpointStateRole, "endpointState");
    names.insert(LeshyPublishedRole, "leshyPublished");
    names.insert(ParkedRoutesRole, "parkedRoutes");
    names.insert(LastRecoveryRole, "lastRecovery");
    names.insert(LastErrorRole, "lastError");
    names.insert(DesiredEnabledRole, "desiredEnabled");
    names.insert(OperationRole, "operation");
    names.insert(ValidatedUnderlayEpochRole, "validatedUnderlayEpoch");
    names.insert(RecoveryActionRole, "recoveryAction");
    names.insert(RecoveryReasonRole, "recoveryReason");
    names.insert(RecoveryAttemptRole, "recoveryAttempt");
    names.insert(NextRetryAtRole, "nextRetryAt");
    names.insert(ValidationStateRole, "validationState");
    names.insert(ValidationReasonRole, "validationReason");
    names.insert(ConfiguredEndpointsRole, "configuredEndpoints");
    names.insert(LiveEndpointsRole, "liveEndpoints");
    names.insert(LeshyZoneRole, "leshyZone");
    names.insert(ParkingActiveRole, "parkingActive");
    names.insert(AvailableActionsRole, "availableActions");
    names.insert(RouteReadyRole, "routeReady");
    names.insert(InterfaceIndexRole, "interfaceIndex");
    names.insert(InterfaceMtuRole, "interfaceMtu");
    names.insert(SessionConnectedRole, "sessionConnected");
    names.insert(SessionRxBytesRole, "sessionRxBytes");
    names.insert(SessionTxBytesRole, "sessionTxBytes");
    names.insert(SessionEndpointRole, "sessionEndpoint");
    names.insert(LastHandshakeAgeMsRole, "lastHandshakeAgeMs");
    names.insert(ResolvedEndpointsRole, "resolvedEndpoints");
    names.insert(EndpointLastErrorRole, "endpointLastError");
    names.insert(AppliedUnderlayEpochRole, "appliedUnderlayEpoch");
    names.insert(ParkedPrefixesRole, "parkedPrefixes");
    names.insert(PidRole, "pid");
    names.insert(LeshyIpv6Role, "leshyIpv6");
    names.insert(GenerationRole, "generation");
    names.insert(Ipv4ConfiguredRole, "ipv4Configured");
    names.insert(Ipv4ReadyRole, "ipv4Ready");
    names.insert(Ipv4ReasonRole, "ipv4Reason");
    names.insert(Ipv6ConfiguredRole, "ipv6Configured");
    names.insert(Ipv6ReadyRole, "ipv6Ready");
    names.insert(Ipv6ReasonRole, "ipv6Reason");
    names.insert(SelectedRoutesRole, "selectedRoutes");
    return names;
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

QVariantMap RoleListModel::toVariantMap(const RoleSnapshot &item) const
{
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
        // leshyIpv6 was missing here: the role drawer reads Core.roles.get() and
        // fell back to "enabled" when the key was absent, so a core-reported
        // leshy_ipv6=false rendered as enabled.
        {"leshyIpv6", item.leshyIpv6},
        {"ipv4Configured", item.ipv4Configured},
        {"ipv4Ready", item.ipv4Ready},
        {"ipv4Reason", item.ipv4Reason},
        {"ipv6Configured", item.ipv6Configured},
        {"ipv6Ready", item.ipv6Ready},
        {"ipv6Reason", item.ipv6Reason},
        {"selectedRoutes", item.selectedRoutes},
        {"generation", item.generation}
    };
}

QVariantMap RoleListModel::get(int row) const
{
    if (row < 0 || row >= m_roles.size())
        return {};

    return toVariantMap(m_roles.at(row));
}

// 09G-W2.1: resolve a role by its stable id; an absent id yields an empty
// map and a -1 row, never a stale row index.
QVariantMap RoleListModel::getById(const QString &id) const
{
    for (const auto &item : m_roles) {
        if (item.id == id)
            return toVariantMap(item);
    }
    return {};
}

int RoleListModel::rowForId(const QString &id) const
{
    for (int row = 0; row < m_roles.size(); ++row) {
        if (m_roles.at(row).id == id)
            return row;
    }
    return -1;
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
