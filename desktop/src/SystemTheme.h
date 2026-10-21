#pragma once

#include <QObject>
#include <QPalette>

#include <optional>

class QApplication;
class QTimer;

class SystemTheme final : public QObject
{
    Q_OBJECT
    Q_PROPERTY(bool dark READ dark NOTIFY themeChanged)

public:
    explicit SystemTheme(QApplication *app, QObject *parent = nullptr);

    bool dark() const;
    Q_INVOKABLE void setDark(bool dark);
    static bool paletteHasCompleteDarkSurfaces(const QPalette &palette);
    static QPalette coherentDarkPalette(const QPalette &source);
    static QPalette coherentLightPalette(const QPalette &source);

signals:
    void themeChanged();

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
