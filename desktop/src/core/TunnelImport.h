#pragma once

#include <QString>
#include <QVariantMap>

// Result of classifying what the user pasted into the "add tunnel" field.
//
// The UI offers one field instead of a form: people paste either a share link
// (vless://...), a gateway URL for OpenConnect, or a ready JSON configuration. The kind is
// shown to the user before anything is written, so a mis-paste is visible immediately.
struct TunnelImport
{
    QString kind;      // "vless-link" | "openconnect-gateway" | "json" | "unknown"
    QString summary;   // short human readable description of what was recognised
    QVariantMap spec;  // ready for CoreBackend::createTunnel, empty when nothing was recognised
    QString error;     // why the text could not be used, empty on success
};

TunnelImport ParseTunnelImport(const QString &text);
