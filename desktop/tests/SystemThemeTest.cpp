#include "SystemTheme.h"

#include <QApplication>
#include <QColor>
#include <QStyle>
#include <QSettings>
#include <QTemporaryDir>
#include <QTest>

class SystemThemeTest final : public QObject
{
    Q_OBJECT

private slots:
    void lightPaletteHasCoherentSurfaces()
    {
        const QPalette light = SystemTheme::coherentLightPalette(qApp->style()->standardPalette());
        QVERIFY(light.color(QPalette::Window).lightness() >= 128);
        QVERIFY(light.color(QPalette::Base).lightness() >= 128);
        QVERIFY(light.color(QPalette::Text).lightness() < 128);
        QCOMPARE(light.color(QPalette::Highlight), QColor(QStringLiteral("#CD761D")));
    }

    void selectedThemePersists()
    {
        QTemporaryDir dir;
        QVERIFY(dir.isValid());
        const QByteArray oldOverride = qgetenv("KIKIMORA_THEME");
        qunsetenv("KIKIMORA_THEME");
        QSettings::setDefaultFormat(QSettings::IniFormat);
        QSettings::setPath(QSettings::IniFormat, QSettings::UserScope, dir.path());
        qApp->setOrganizationName("KikimoraThemeTest");
        qApp->setApplicationName("theme-settings");
        {
            SystemTheme theme(qApp);
            theme.setDark(false);
            QVERIFY(!theme.dark());
            QCOMPARE(qApp->palette().color(QPalette::Window), QColor("#F7F5F2"));
        }
        {
            SystemTheme theme(qApp);
            QVERIFY(!theme.dark());
            theme.setDark(true);
            QVERIFY(theme.dark());
        }
        {
            SystemTheme theme(qApp);
            QVERIFY(theme.dark());
        }
        if (oldOverride.isNull())
            qunsetenv("KIKIMORA_THEME");
        else
            qputenv("KIKIMORA_THEME", oldOverride);
    }

    void darkPaletteHasCoherentSurfaces()
    {
        const QPalette source = qApp->style()->standardPalette();
        const QPalette dark = SystemTheme::coherentDarkPalette(source);
        QVERIFY(SystemTheme::paletteHasCompleteDarkSurfaces(dark));
        QVERIFY(dark.color(QPalette::Window).lightness() < 128);
        QVERIFY(dark.color(QPalette::Base).lightness() < 128);
        QVERIFY(dark.color(QPalette::Text).lightness() >= 128);
        QCOMPARE(dark.color(QPalette::Highlight), QColor(QStringLiteral("#FBB26A")));
        QCOMPARE(dark.color(QPalette::HighlightedText), QColor(17, 17, 20));
    }
};

QTEST_MAIN(SystemThemeTest)
#include "SystemThemeTest.moc"
