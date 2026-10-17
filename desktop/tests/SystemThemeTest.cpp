#include "SystemTheme.h"

#include <QApplication>
#include <QColor>
#include <QStyle>
#include <QTest>

class SystemThemeTest final : public QObject
{
    Q_OBJECT

private slots:
    void darkPaletteHasCoherentSurfaces()
    {
        const QPalette source = qApp->style()->standardPalette();
        const QPalette dark = SystemTheme::coherentDarkPalette(source);
        QVERIFY(SystemTheme::paletteHasCompleteDarkSurfaces(dark));
        QVERIFY(dark.color(QPalette::Window).lightness() < 128);
        QVERIFY(dark.color(QPalette::Base).lightness() < 128);
        QVERIFY(dark.color(QPalette::Text).lightness() >= 128);
    }
};

QTEST_MAIN(SystemThemeTest)
#include "SystemThemeTest.moc"
