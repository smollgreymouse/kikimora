#pragma once

#include <QObject>
#include <QPalette>

#include <optional>

class QApplication;
class QTimer;

class SystemTheme final : public QObject
{
    Q_OBJECT

public:
    explicit SystemTheme(QApplication *app, QObject *parent = nullptr);

    static bool paletteHasCompleteDarkSurfaces(const QPalette &palette);
    static QPalette coherentDarkPalette(const QPalette &source);

public slots:
    void refresh();

private:
    static std::optional<bool> environmentOverride();
    static std::optional<bool> gnomePrefersDark();
    static std::optional<bool> qtPrefersDark(const QApplication *app);
    static bool systemPrefersDark(const QApplication *app);

    QApplication *m_app;
    QPalette m_platformPalette;
    QTimer *m_timer;
    std::optional<bool> m_lastDark;
};
