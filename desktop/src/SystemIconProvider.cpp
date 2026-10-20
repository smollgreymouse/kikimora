#include "SystemIconProvider.h"

#include <QApplication>
#include <QGuiApplication>
#include <QIcon>
#include <QPainter>
#include <QPen>
#include <QStyle>

namespace {

// A drawn gear: Windows ships no icon theme, so the style fallback for "settings" looked like
// a laptop. Drawing it keeps the tab icon honest on every platform.
QPixmap gearPixmap(int size)
{
    QPixmap pixmap(size, size);
    pixmap.fill(Qt::transparent);

    QPainter painter(&pixmap);
    painter.setRenderHint(QPainter::Antialiasing);
    QPen pen(QGuiApplication::palette().color(QPalette::WindowText));
    pen.setWidthF(qMax(1.5, size / 12.0));
    pen.setCapStyle(Qt::FlatCap);
    painter.setPen(pen);
    painter.setBrush(Qt::NoBrush);

    const qreal center = size / 2.0;
    const qreal outer = size * 0.40;
    const qreal inner = size * 0.20;
    painter.drawEllipse(QPointF(center, center), inner, inner);
    for (int tooth = 0; tooth < 8; ++tooth) {
        painter.save();
        painter.translate(center, center);
        painter.rotate(tooth * 45.0);
        painter.drawLine(QPointF(0, -inner * 1.35), QPointF(0, -outer));
        painter.restore();
    }
    return pixmap;
}

QIcon iconForName(const QString &name)
{
    if (name == QStringLiteral("settings"))
        return QIcon(gearPixmap(64));

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
    } else if (name == QStringLiteral("diagnostics")) {
        themeNames = {QStringLiteral("utilities-system-monitor"), QStringLiteral("dialog-information")};
        fallback = QStyle::SP_MessageBoxInformation;
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
