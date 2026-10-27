#include "core/TunnelImport.h"

#include <QJsonDocument>
#include <QJsonObject>
#include <QRegularExpression>
#include <QUrl>
#include <QUrlQuery>

namespace {

TunnelImport classifyJson(const QString &text)
{
    TunnelImport result;
    QJsonParseError parseError;
    const QJsonDocument document = QJsonDocument::fromJson(text.toUtf8(), &parseError);
    if (parseError.error != QJsonParseError::NoError || !document.isObject()) {
        result.kind = QStringLiteral("unknown");
        result.error = QStringLiteral("похоже на JSON, но разобрать не удалось: %1")
                           .arg(parseError.errorString());
        return result;
    }

    QJsonObject object = document.object();
    if (object.contains(QStringLiteral("tunnel")) && object.value(QStringLiteral("tunnel")).isObject())
        object = object.value(QStringLiteral("tunnel")).toObject();

    const QString name = object.value(QStringLiteral("name")).toString().trimmed();
    const QString protocol = object.value(QStringLiteral("protocol")).toString().trimmed();
    if (name.isEmpty() || protocol.isEmpty()) {
        result.kind = QStringLiteral("unknown");
        result.error = QStringLiteral("в JSON нужны поля name и protocol");
        return result;
    }

    result.kind = QStringLiteral("json");
    result.summary = QStringLiteral("JSON-конфиг: %1 (%2)").arg(name, protocol);
    result.spec = object.toVariantMap();
    return result;
}

TunnelImport classifyVlessLink(const QString &text)
{
    TunnelImport result;
    result.kind = QStringLiteral("vless-link");

    const QUrl url(text.trimmed());
    if (!url.isValid() || url.host().isEmpty()) {
        result.error = QStringLiteral("ссылка vless:// не разобрана: нет узла");
        return result;
    }
    const QString uuid = url.userName().trimmed();
    if (uuid.isEmpty()) {
        result.error = QStringLiteral("ссылка vless:// не разобрана: нет UUID");
        return result;
    }
    const int port = url.port() > 0 ? url.port() : 443;
    const QUrlQuery query(url);

    QVariantMap spec;
    const QString fragment = url.fragment().trimmed();
    spec.insert(QStringLiteral("name"), fragment.isEmpty()
                                            ? QStringLiteral("vless-%1").arg(url.host())
                                            : QUrl::fromPercentEncoding(fragment.toUtf8()));
    spec.insert(QStringLiteral("protocol"), QStringLiteral("vless-reality"));
    spec.insert(QStringLiteral("endpoint"), QStringLiteral("%1:%2").arg(url.host()).arg(port));
    // No address: a VLESS link never carries one, and the core assigns a free
    // address from its pool when the spec omits it (create.go).
    spec.insert(QStringLiteral("uuid"), uuid);
    spec.insert(QStringLiteral("server_name"), query.queryItemValue(QStringLiteral("sni")));
    spec.insert(QStringLiteral("public_key"), query.queryItemValue(QStringLiteral("pbk")));
    spec.insert(QStringLiteral("short_id"), query.queryItemValue(QStringLiteral("sid")));
    spec.insert(QStringLiteral("flow"), query.queryItemValue(QStringLiteral("flow")));

    result.summary = QStringLiteral("ссылка VLESS/REALITY: %1 → %2:%3")
                         .arg(spec.value(QStringLiteral("name")).toString(), url.host())
                         .arg(port);
    result.spec = spec;
    if (spec.value(QStringLiteral("server_name")).toString().isEmpty()
        || spec.value(QStringLiteral("public_key")).toString().isEmpty()) {
        result.error = QStringLiteral("в ссылке нет sni или pbk — REALITY без них не поднимется");
    }
    return result;
}

} // namespace

TunnelImport ParseTunnelImport(const QString &text)
{
    const QString trimmed = text.trimmed();
    if (trimmed.isEmpty()) {
        TunnelImport result;
        result.kind = QStringLiteral("unknown");
        result.error = QStringLiteral("вставьте ссылку или JSON-конфиг");
        return result;
    }

    if (trimmed.startsWith(QLatin1Char('{')) || trimmed.startsWith(QLatin1Char('[')))
        return classifyJson(trimmed);

    if (trimmed.startsWith(QStringLiteral("vless://"), Qt::CaseInsensitive))
        return classifyVlessLink(trimmed);

    if (trimmed.startsWith(QStringLiteral("http://"), Qt::CaseInsensitive)
        || trimmed.startsWith(QStringLiteral("https://"), Qt::CaseInsensitive)) {
        TunnelImport result;
        result.kind = QStringLiteral("openconnect-gateway");
        result.summary = QStringLiteral("шлюз OpenConnect: %1").arg(trimmed);
        QVariantMap spec;
        spec.insert(QStringLiteral("name"), QStringLiteral("oc-%1").arg(QUrl(trimmed).host()));
        spec.insert(QStringLiteral("protocol"), QStringLiteral("openconnect"));
        spec.insert(QStringLiteral("gateway"), trimmed);
        result.spec = spec;
        // The core still needs a username and a secret file; it reports that itself.
        return result;
    }

    TunnelImport result;
    result.kind = QStringLiteral("unknown");
    result.error = QStringLiteral(
        "не распознано: ожидается ссылка vless://, адрес шлюза OpenConnect (https://…) "
        "или JSON-конфиг туннеля");
    return result;
}
