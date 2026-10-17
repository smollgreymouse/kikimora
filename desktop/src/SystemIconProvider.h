#pragma once

#include <QQuickImageProvider>

class SystemIconProvider final : public QQuickImageProvider
{
public:
    SystemIconProvider();

    QPixmap requestPixmap(const QString &id, QSize *size,
                          const QSize &requestedSize) override;
};
