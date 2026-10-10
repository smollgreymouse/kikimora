#include "SystemIconProvider.h"

#include <QApplication>
#include <QIcon>
#include <QStyle>

namespace {

QIcon iconForName(const QString &name)
{
    QStringList themeNames;
    QStyle::StandardPixmap fallback = QStyle::SP_FileDialogDetailedView;
    if (name == QStringLiteral("home")) {
        themeNames = {QStringLiteral("go-home"), QStringLiteral("user-home")};
        fallback = QStyle::SP_DirHomeIcon;
    } else if (name == QStringLiteral("profiles")) {
        themeNames = {QStringLiteral("view-list-details"), QStringLiteral("view-list")};
        fallback = QStyle::SP_FileDialogListView;
    } else if (name == QStringLiteral("routing")) {
        themeNames = {QStringLiteral("network-vpn"), QStringLiteral("network-wired")};
        fallback = QStyle::SP_DriveNetIcon;
    } else if (name == QStringLiteral("settings")) {
        themeNames = {QStringLiteral("preferences-system"), QStringLiteral("emblem-system")};
        fallback = QStyle::SP_FileDialogDetailedView;
    }

    for (const auto &themeName : themeNames) {
        const QIcon icon = QIcon::fromTheme(themeName);
        if (!icon.isNull())
            return icon;
    }
    return qApp->style()->standardIcon(fallback);
}

} // namespace

SystemIconProvider::SystemIconProvider()
    : QQuickImageProvider(QQuickImageProvider::Pixmap)
{
}

QPixmap SystemIconProvider::requestPixmap(const QString &id, QSize *size,
                                          const QSize &requestedSize)
{
    QSize target = requestedSize;
    if (!target.isValid() || target.width() <= 0 || target.height() <= 0)
        target = QSize(24, 24);

    const QPixmap pixmap = iconForName(id).pixmap(target);
    if (size)
        *size = pixmap.size();
    return pixmap;
}
