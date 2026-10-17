#include "SystemTheme.h"

#include <QApplication>
#include <QColor>
#include <QProcess>
#include <QStandardPaths>
#include <QStyle>
#include <QStyleHints>
#include <QTimer>

namespace {

bool isDark(const QColor &color)
{
    return color.lightness() < 128;
}

bool isLight(const QColor &color)
{
    return color.lightness() >= 128;
}

void setGroupPalette(QPalette &palette, QPalette::ColorGroup group,
                     const QColor &window, const QColor &base,
                     const QColor &alternate, const QColor &button,
                     const QColor &text, const QColor &muted,
                     const QColor &highlight)
{
    palette.setColor(group, QPalette::Window, window);
    palette.setColor(group, QPalette::WindowText, text);
    palette.setColor(group, QPalette::Base, base);
    palette.setColor(group, QPalette::AlternateBase, alternate);
    palette.setColor(group, QPalette::ToolTipBase, alternate);
    palette.setColor(group, QPalette::ToolTipText, text);
    palette.setColor(group, QPalette::Text, text);
    palette.setColor(group, QPalette::Button, button);
    palette.setColor(group, QPalette::ButtonText, text);
    palette.setColor(group, QPalette::BrightText, QColor(255, 255, 255));
    palette.setColor(group, QPalette::PlaceholderText, muted);
    palette.setColor(group, QPalette::Highlight, highlight);
    palette.setColor(group, QPalette::HighlightedText, QColor(255, 255, 255));
}

} // namespace

SystemTheme::SystemTheme(QApplication *app, QObject *parent)
    : QObject(parent)
    , m_app(app)
    , m_platformPalette(app->style()->standardPalette())
    , m_timer(new QTimer(this))
{
    m_timer->setInterval(3000);
    connect(m_timer, &QTimer::timeout, this, &SystemTheme::refresh);
    refresh();
    m_timer->start();
}

std::optional<bool> SystemTheme::environmentOverride()
{
    QByteArray value = qgetenv("KIKIMORA_THEME").trimmed().toLower();
    if (value.isEmpty())
        value = qgetenv("HARR_CODEX_THEME").trimmed().toLower();
    if (value == "dark")
        return true;
    if (value == "light")
        return false;
    return std::nullopt;
}

std::optional<bool> SystemTheme::gnomePrefersDark()
{
    const QString gsettings = QStandardPaths::findExecutable(QStringLiteral("gsettings"));
    if (gsettings.isEmpty())
        return std::nullopt;

    QProcess process;
    process.start(gsettings, {QStringLiteral("get"), QStringLiteral("org.gnome.desktop.interface"),
                              QStringLiteral("color-scheme")});
    if (!process.waitForFinished(1000) || process.exitCode() != 0)
        return std::nullopt;

    const QString value = QString::fromUtf8(process.readAllStandardOutput()).trimmed()
                              .remove(QLatin1Char('\''))
                              .remove(QLatin1Char('"'))
                              .toLower();
    if (value == QStringLiteral("prefer-dark"))
        return true;
    if (value == QStringLiteral("default") || value == QStringLiteral("prefer-light"))
        return false;
    return std::nullopt;
}

std::optional<bool> SystemTheme::qtPrefersDark(const QApplication *app)
{
    if (!app)
        return std::nullopt;
    const auto scheme = app->styleHints()->colorScheme();
    if (scheme == Qt::ColorScheme::Dark)
        return true;
    if (scheme == Qt::ColorScheme::Light)
        return false;
    return std::nullopt;
}

bool SystemTheme::systemPrefersDark(const QApplication *app)
{
    if (const auto override = environmentOverride(); override.has_value())
        return *override;
    if (const auto gnome = gnomePrefersDark(); gnome.has_value())
        return *gnome;
    if (const auto qt = qtPrefersDark(app); qt.has_value())
        return *qt;
    return app && isDark(app->palette().color(QPalette::Window));
}

bool SystemTheme::paletteHasCompleteDarkSurfaces(const QPalette &palette)
{
    const QPalette::ColorRole surfaces[] = {
        QPalette::Window, QPalette::Base, QPalette::AlternateBase,
        QPalette::Button, QPalette::ToolTipBase,
    };
    const QPalette::ColorRole text[] = {
        QPalette::WindowText, QPalette::Text, QPalette::ButtonText,
        QPalette::ToolTipText,
    };
    const QPalette::ColorGroup groups[] = {
        QPalette::Active, QPalette::Inactive,
    };
    for (const auto group : groups) {
        for (const auto role : surfaces) {
            if (!isDark(palette.color(group, role)))
                return false;
        }
        for (const auto role : text) {
            if (!isLight(palette.color(group, role)))
                return false;
        }
    }
    return true;
}

QPalette SystemTheme::coherentDarkPalette(const QPalette &source)
{
    QPalette palette(source);
    QColor highlight = source.color(QPalette::Active, QPalette::Highlight);
    if (!highlight.isValid() || highlight.lightness() < 55)
        highlight = QColor(53, 132, 228);

    setGroupPalette(palette, QPalette::Active,
                    QColor(34, 35, 38), QColor(27, 28, 31), QColor(42, 43, 47),
                    QColor(47, 48, 52), QColor(238, 238, 240), QColor(154, 155, 160), highlight);
    setGroupPalette(palette, QPalette::Inactive,
                    QColor(34, 35, 38), QColor(27, 28, 31), QColor(42, 43, 47),
                    QColor(47, 48, 52), QColor(238, 238, 240), QColor(154, 155, 160), highlight);
    setGroupPalette(palette, QPalette::Disabled,
                    QColor(34, 35, 38), QColor(27, 28, 31), QColor(42, 43, 47),
                    QColor(39, 40, 43), QColor(126, 127, 132), QColor(104, 105, 110),
                    QColor(70, 76, 86));
    return palette;
}

void SystemTheme::refresh()
{
    const bool dark = systemPrefersDark(m_app);
    if (m_lastDark.has_value() && *m_lastDark == dark)
        return;
    m_lastDark = dark;

    if (dark) {
        if (!paletteHasCompleteDarkSurfaces(m_app->palette()))
            m_app->setPalette(coherentDarkPalette(m_platformPalette));
    } else {
        m_app->setPalette(m_platformPalette);
    }
}
