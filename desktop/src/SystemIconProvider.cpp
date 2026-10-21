#include "SystemIconProvider.h"

#include <QApplication>
#include <QPainter>
#include <QPainterPath>
#include <QPalette>
#include <QPen>

namespace {

QPixmap navGlyphPixmap(const QString &name, int size)
{
    QPixmap pixmap(size, size);
    pixmap.fill(Qt::transparent);

    QPainter painter(&pixmap);
    painter.setRenderHint(QPainter::Antialiasing);

    const qreal s = size;
    QPen pen(Qt::white);
    pen.setWidthF(qMax(1.5, s * 0.075));
    pen.setCapStyle(Qt::RoundCap);
    pen.setJoinStyle(Qt::RoundJoin);
    painter.setPen(pen);
    painter.setBrush(Qt::NoBrush);

    if (name == QStringLiteral("home")) {
        QPainterPath roof;
        roof.moveTo(s * 0.20, s * 0.47);
        roof.lineTo(s * 0.50, s * 0.22);
        roof.lineTo(s * 0.80, s * 0.47);
        painter.drawPath(roof);
        painter.drawRoundedRect(QRectF(s * 0.27, s * 0.43, s * 0.46, s * 0.38),
                                s * 0.05, s * 0.05);
        painter.drawLine(QPointF(s * 0.50, s * 0.81), QPointF(s * 0.50, s * 0.61));
    } else if (name == QStringLiteral("profiles")) {
        const qreal cell = s * 0.22;
        const qreal gap = s * 0.12;
        const qreal x = (s - (cell * 2 + gap)) / 2.0;
        const qreal y = x;
        for (int row = 0; row < 2; ++row) {
            for (int column = 0; column < 2; ++column) {
                painter.drawRoundedRect(QRectF(x + column * (cell + gap),
                                               y + row * (cell + gap),
                                               cell, cell),
                                        s * 0.04, s * 0.04);
            }
        }
    } else if (name == QStringLiteral("routing")) {
        // A compact route tree: one uplink feeding two endpoints.
        painter.drawRoundedRect(QRectF(s * 0.38, s * 0.16, s * 0.24, s * 0.20),
                                s * 0.04, s * 0.04);
        painter.drawLine(QPointF(s * 0.50, s * 0.36), QPointF(s * 0.50, s * 0.58));
        painter.drawLine(QPointF(s * 0.25, s * 0.58), QPointF(s * 0.75, s * 0.58));
        painter.drawLine(QPointF(s * 0.25, s * 0.58), QPointF(s * 0.25, s * 0.68));
        painter.drawLine(QPointF(s * 0.75, s * 0.58), QPointF(s * 0.75, s * 0.68));
        painter.drawEllipse(QPointF(s * 0.25, s * 0.77), s * 0.09, s * 0.09);
        painter.drawEllipse(QPointF(s * 0.75, s * 0.77), s * 0.09, s * 0.09);
    } else if (name == QStringLiteral("settings")) {
        const QPointF center(s * 0.50, s * 0.50);
        const qreal inner = s * 0.16;
        const qreal middle = s * 0.27;
        const qreal outer = s * 0.38;
        painter.drawEllipse(center, inner, inner);
        painter.drawEllipse(center, middle, middle);
        for (int tooth = 0; tooth < 8; ++tooth) {
            painter.save();
            painter.translate(center);
            painter.rotate(tooth * 45.0);
            painter.drawLine(QPointF(0, -middle), QPointF(0, -outer));
            painter.restore();
        }
    } else if (name == QStringLiteral("diagnostics")) {
        painter.drawRoundedRect(QRectF(s * 0.16, s * 0.20, s * 0.68, s * 0.50),
                                s * 0.05, s * 0.05);
        QPainterPath pulse;
        pulse.moveTo(s * 0.25, s * 0.47);
        pulse.lineTo(s * 0.37, s * 0.47);
        pulse.lineTo(s * 0.44, s * 0.36);
        pulse.lineTo(s * 0.53, s * 0.57);
        pulse.lineTo(s * 0.61, s * 0.43);
        pulse.lineTo(s * 0.75, s * 0.43);
        painter.drawPath(pulse);
        painter.drawLine(QPointF(s * 0.39, s * 0.80), QPointF(s * 0.61, s * 0.80));
        painter.drawLine(QPointF(s * 0.50, s * 0.70), QPointF(s * 0.50, s * 0.80));
    } else {
        painter.drawEllipse(QPointF(s * 0.50, s * 0.50), s * 0.25, s * 0.25);
    }

    return pixmap;
}

QPixmap tintPixmap(const QPixmap &source, const QColor &color)
{
    if (source.isNull() || !color.isValid())
        return source;

    QPixmap tinted(source.size());
    tinted.fill(Qt::transparent);

    QPainter painter(&tinted);
    painter.drawPixmap(0, 0, source);
    painter.setCompositionMode(QPainter::CompositionMode_SourceIn);
    painter.fillRect(tinted.rect(), color);
    return tinted;
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

    QString iconName = id;
    QColor tint = qApp->palette().color(QPalette::WindowText);
    if (iconName.startsWith(QStringLiteral("accent/"))) {
        iconName.remove(0, QStringLiteral("accent/").size());
        tint = QColor(QStringLiteral("#FBB26A"));
    } else if (iconName.startsWith(QStringLiteral("muted/"))) {
        iconName.remove(0, QStringLiteral("muted/").size());
        tint = QColor(QStringLiteral("#D7D5D5"));
    }

    QPixmap pixmap = navGlyphPixmap(iconName, qMin(target.width(), target.height()));
    pixmap = tintPixmap(pixmap, tint);

    if (size)
        *size = pixmap.size();
    return pixmap;
}
