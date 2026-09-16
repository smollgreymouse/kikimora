#include <QGuiApplication>
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQuickStyle>

#include "core/FakeCoreBackend.h"

int main(int argc, char *argv[])
{
    QGuiApplication app(argc, argv);
    app.setApplicationName(QStringLiteral("Kikimora"));
    app.setOrganizationName(QStringLiteral("Kikimora"));

    QQuickStyle::setStyle(QStringLiteral("Basic"));

    FakeCoreBackend core;
    QQmlApplicationEngine engine;
    engine.rootContext()->setContextProperty(QStringLiteral("Core"), &core);

    const QUrl url(QStringLiteral("qrc:/qml/Main.qml"));
    QObject::connect(&engine, &QQmlApplicationEngine::objectCreated,
                     &app, [url](QObject *obj, const QUrl &objUrl) {
        if (!obj && objUrl == url)
            QCoreApplication::exit(-1);
    }, Qt::QueuedConnection);

    engine.load(url);
    return app.exec();
}
