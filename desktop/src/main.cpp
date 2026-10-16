#include <QApplication>
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQuickStyle>
#include <QCommandLineParser>
#include <QLocale>
#include <QTranslator>
#include <QWindow>
#include "DesktopIntegration.h"
#include "SystemIconProvider.h"
#include "SystemTheme.h"
#include "core/FakeCoreBackend.h"
#include "core/RealCoreClient.h"

int main(int argc, char *argv[])
{
    QApplication app(argc, argv);
    app.setApplicationName(QStringLiteral("Kikimora"));
    app.setOrganizationName(QStringLiteral("Kikimora"));
    app.setQuitOnLastWindowClosed(false);

    // Translations are optional: when the build has LinguistTools the
    // catalogues are embedded under :/i18n/. KIKIMORA_LOCALE overrides the
    // system locale, which is what the UI tests and portable installs use.
    QTranslator translator;
    const QString localeName = qEnvironmentVariable("KIKIMORA_LOCALE", QLocale::system().name());
    if (translator.load(QStringLiteral(":/i18n/kikimora_") + localeName)
        || (!localeName.isEmpty()
            && translator.load(QStringLiteral(":/i18n/kikimora_") + localeName.left(2))))
        QCoreApplication::installTranslator(&translator);

    const QByteArray styleOverride = qgetenv("KIKIMORA_QT_STYLE");
    if (!styleOverride.isEmpty())
        QQuickStyle::setStyle(QString::fromUtf8(styleOverride));

    SystemTheme systemTheme(&app);

    QCommandLineParser parser;
    parser.setApplicationDescription("Kikimora VPN client");
    parser.addHelpOption();
    QCommandLineOption fakeOption(QStringList() << "fake", "Use fake backend for testing");
    QCommandLineOption socketOption(QStringList() << "socket", "Core Unix socket", "path",
                                     RealCoreClient::defaultSocketPath());
    parser.addOption(fakeOption);
    parser.addOption(socketOption);
    parser.process(app);

    bool useFake = parser.isSet(fakeOption);
    const QString socketPath = parser.value(socketOption);
#if defined(Q_OS_WIN)
    useFake = true;
#endif

    CoreBackend *core;
    if (useFake) {
        core = new FakeCoreBackend(&app);
    } else {
        core = new RealCoreClient(socketPath, &app);
    }

    DesktopIntegration desktop(core, &app);
    QQmlApplicationEngine engine;
    engine.addImageProvider(QStringLiteral("system"), new SystemIconProvider);
    engine.rootContext()->setContextProperty(QStringLiteral("Core"), core);
    engine.rootContext()->setContextProperty(QStringLiteral("Desktop"), &desktop);

    const QUrl url(QStringLiteral("qrc:/qml/Main.qml"));
    QObject::connect(&engine, &QQmlApplicationEngine::objectCreated,
                     &app, [url](QObject *obj, const QUrl &objUrl) {
        if (!obj && objUrl == url)
            QCoreApplication::exit(-1);
    }, Qt::QueuedConnection);

    engine.load(url);
    if (engine.rootObjects().isEmpty()) return -1;
    desktop.setWindow(qobject_cast<QWindow *>(engine.rootObjects().constFirst()));
    return app.exec();
}
