#include <QApplication>
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQuickStyle>
#include <QCommandLineParser>
#include <QDebug>
#include <QIcon>
#include <QDir>
#include <QFile>
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

    // The window, the taskbar and the tray all use the packaged icon.
    app.setWindowIcon(QIcon(QStringLiteral(":/resources/kikimora.png")));

    // Translations are optional: when the build has LinguistTools the
    // catalogues are embedded under :/i18n/. KIKIMORA_LOCALE overrides the
    // system locale, which is what the UI tests and portable installs use.
    QTranslator translator;
    const QString localeName = qEnvironmentVariable("KIKIMORA_LOCALE", QLocale::system().name());
    if (translator.load(QStringLiteral(":/i18n/kikimora_") + localeName)
        || (!localeName.isEmpty()
            && translator.load(QStringLiteral(":/i18n/kikimora_") + localeName.left(2))))
        QCoreApplication::installTranslator(&translator);

    // The UI customises Control backgrounds (KButton, KIconButton, ...), which the
    // native Windows style refuses to honour: without Basic the window renders with
    // warnings and wrong colours. KIKIMORA_QT_STYLE keeps its escape hatch.
    QQuickStyle::setStyle(QString::fromUtf8(
        qgetenv("KIKIMORA_QT_STYLE").isEmpty() ? QByteArrayLiteral("Basic")
                                              : qgetenv("KIKIMORA_QT_STYLE")));

    SystemTheme systemTheme(&app);

    QCommandLineParser parser;
    parser.setApplicationDescription("Kikimora VPN client");
    parser.addHelpOption();
    QCommandLineOption fakeOption(QStringList() << "fake", "Use fake backend for testing");
    // Autostart launches with this flag: the window stays in the tray until asked for.
    QCommandLineOption backgroundOption(QStringList() << "background", "Start hidden in the tray");
    // Installers and tests flip autostart without a GUI: --set-autostart on|off.
    QCommandLineOption autostartOption(QStringList() << "set-autostart", "Enable start with the session", "state");
    QCommandLineOption socketOption(QStringList() << "socket", "Core Unix socket", "path",
                                     RealCoreClient::defaultSocketPath());
    parser.addOption(fakeOption);
    parser.addOption(backgroundOption);
    parser.addOption(autostartOption);
    parser.addOption(socketOption);
    parser.process(app);

    bool useFake = parser.isSet(fakeOption);
    const QString socketPath = parser.value(socketOption);

    // Packaging check: report the chosen transport into a file and exit. A windowed
    // executable has no console to print to, so the result goes to the path named by
    // KIKIMORA_UI_PRINT_BACKEND (or %TEMP%/kikimora-ui-backend.txt when it is "1").
    if (qEnvironmentVariableIsSet("KIKIMORA_UI_PRINT_BACKEND")) {
        const QString line = QStringLiteral("backend=%1 socket=%2 available=%3")
                                 .arg(useFake ? QStringLiteral("fake") : QStringLiteral("real-core"),
                                      socketPath,
                                      RealCoreClient::isSocketAvailable(socketPath)
                                          ? QStringLiteral("true")
                                          : QStringLiteral("false"));
        const QString target = qEnvironmentVariable("KIKIMORA_UI_PRINT_BACKEND");
        const QString path = (target.isEmpty() || target == QStringLiteral("1"))
                                 ? QDir::tempPath() + QStringLiteral("/kikimora-ui-backend.txt")
                                 : target;
        QFile file(path);
        if (file.open(QIODevice::WriteOnly | QIODevice::Truncate))
            file.write(line.toUtf8() + '\n');
        return 0;
    }

    CoreBackend *core;
    if (useFake) {
        core = new FakeCoreBackend(&app);
    } else {
        core = new RealCoreClient(socketPath, &app);
    }

    DesktopIntegration desktop(core, &app);

    if (parser.isSet(autostartOption)) {
        const QString state = parser.value(autostartOption).trimmed().toLower();
        const bool enable = state != QStringLiteral("off") && state != QStringLiteral("0")
                            && state != QStringLiteral("false");
        desktop.setAutostartEnabled(enable);
        const QString line = QStringLiteral("autostart=%1 target=%2")
                                 .arg(enable ? QStringLiteral("on") : QStringLiteral("off"),
                                      desktop.autostartTarget());
        QFile report(qEnvironmentVariable("KIKIMORA_UI_REPORT"));
        if (report.open(QIODevice::WriteOnly | QIODevice::Truncate))
            report.write(line.toUtf8() + '\n');
        return 0;
    }
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
    if (parser.isSet(backgroundOption))
        desktop.hideWindow();
    return app.exec();
}
