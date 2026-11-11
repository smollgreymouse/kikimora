#pragma once

#include <QApplication>
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQuickWindow>
#include <QString>

#include "DesktopIntegration.h"
#include "SystemTheme.h"
#include "SystemIconProvider.h"

// One production-equivalent QML bootstrap for every full-QML suite (U4).
//
// Production main.cpp pins the Basic style before the first QML import and
// installs Core, Desktop, ThemeManager and SystemIconProvider into the engine
// root context. Every test that loads qrc:/qml/Main.qml must reproduce that
// exact context, or the suite invents failure modes the product never has
// ("ThemeManager is not defined", platform-style control differences).
namespace UiTest {

// QQuickStyle::setStyle() must run before the first QML load of the process.
// Calling it again later prints the ordering warning, so this is idempotent.
void ensureProductionBootstrap();

// Loads qrc:/qml/Main.qml against a real DesktopIntegration/SystemTheme
// exactly like production main.cpp. The backend is the test's own instance;
// its type decides which backend the QML sees.
template <typename CoreT>
struct UiTestFixture
{
    CoreT core;
    DesktopIntegration desktop{&core};
    SystemTheme theme{qApp};
    QQmlApplicationEngine engine;
    QObject *root = nullptr;
    QQuickWindow *window = nullptr;

    // FakeCoreBackend-style default construction.
    UiTestFixture()
    {
        load();
    }

    // RealCoreClient against a specific control socket.
    explicit UiTestFixture(const QString &socketPath)
        : core(socketPath, nullptr)
    {
        load();
    }

private:
    void load()
    {
        ensureProductionBootstrap();
        engine.addImageProvider(QStringLiteral("system"), new SystemIconProvider);
        engine.rootContext()->setContextProperty(QStringLiteral("Core"), &core);
        engine.rootContext()->setContextProperty(QStringLiteral("Desktop"), &desktop);
        engine.rootContext()->setContextProperty(QStringLiteral("ThemeManager"), &theme);
        engine.load(QUrl(QStringLiteral("qrc:/qml/Main.qml")));
        if (!engine.rootObjects().isEmpty()) {
            root = engine.rootObjects().constFirst();
            window = qobject_cast<QQuickWindow *>(root);
        }
    }
};

} // namespace UiTest
