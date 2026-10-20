#include "core/CoreBackend.h"

QString CoreBackend::describeTunnelImport(const QString &text) const
{
    const TunnelImport parsed = ParseTunnelImport(text);
    return parsed.error.isEmpty() ? parsed.summary : parsed.error;
}
