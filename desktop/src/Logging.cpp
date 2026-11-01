#include "Logging.h"

#include <QCoreApplication>
#include <QDate>
#include <QDateTime>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QMutex>
#include <QStandardPaths>
#include <QTextStream>
#include <cstdio>

namespace {

QMutex logMutex;
QString logPath;
bool logPathResolved = false;
bool logEnabled = false;

// defaultLogPath keeps the UI log next to the other per-user data (AppData on
// Windows, ~/.local/share on Linux, Application Support on macOS) so the core
// collector can find it with one glob. Three daily files are kept: the capture
// itself only needs the last day, the extra two cover a machine that sat idle.
QString defaultLogPath()
{
    // Resolve the per-user data directory from the environment instead of
    // QStandardPaths: a session-0 service context has no shell folders, and an
    // empty path there silently disabled the UI log entirely.
#ifdef Q_OS_WIN
    QString base = qEnvironmentVariable("LOCALAPPDATA");
    if (base.isEmpty())
        base = QDir::homePath() + QStringLiteral("/AppData/Local");
    const QString dir = base + QStringLiteral("/Kikimora/logs");
#elif defined(Q_OS_MACOS)
    const QString dir = QDir::homePath() + QStringLiteral("/Library/Application Support/Kikimora/logs");
#else
    QString base = qEnvironmentVariable("XDG_DATA_HOME");
    if (base.isEmpty())
        base = QDir::homePath() + QStringLiteral("/.local/share");
    const QString dir = base + QStringLiteral("/Kikimora/logs");
#endif
    QDir().mkpath(dir);
    const QDateTime cutoff = QDateTime::currentDateTime().addDays(-3);
    const QFileInfoList stale = QDir(dir).entryInfoList({QStringLiteral("kikimora-ui-*.log")}, QDir::Files);
    for (const QFileInfo &info : stale) {
        if (info.lastModified() < cutoff)
            QFile::remove(info.absoluteFilePath());
    }
    return dir + QStringLiteral("/kikimora-ui-")
           + QDate::currentDate().toString(QStringLiteral("yyyyMMdd")) + QStringLiteral(".log");
}

void resolvePath()
{
    if (logPathResolved) return;
    logPathResolved = true;
    logPath = qEnvironmentVariable("KIKIMORA_UI_DEBUG_LOG");
    if (logPath.isEmpty())
        logPath = defaultLogPath();
    logEnabled = !logPath.isEmpty();
}

void writeLine(const QString &message)
{
    QMutexLocker locker(&logMutex);
    resolvePath();
    if (!logEnabled) return;

    QFile file(logPath);
    if (!file.open(QIODevice::WriteOnly | QIODevice::Append | QIODevice::Text)) {
        std::fprintf(stderr, "kikimora-ui: cannot open log %s\n", qPrintable(logPath));
        return;
    }
    QTextStream stream(&file);
    stream << QDateTime::currentDateTime().toString(Qt::ISODateWithMs) << ' ' << message << '\n';
}

void messageHandler(QtMsgType type, const QMessageLogContext &, const QString &message)
{
    const char *level = "debug";
    switch (type) {
    case QtInfoMsg: level = "info"; break;
    case QtWarningMsg: level = "warn"; break;
    case QtCriticalMsg: level = "error"; break;
    case QtFatalMsg: level = "fatal"; break;
    default: break;
    }
    writeLine(QStringLiteral("[%1] %2").arg(QLatin1String(level), message));
    std::fprintf(stderr, "%s\n", qPrintable(message));
}

} // namespace

bool uiLogEnabled()
{
    QMutexLocker locker(&logMutex);
    resolvePath();
    return logEnabled;
}

void uiLog(const QString &message)
{
    writeLine(message);
}

void installUiLogging()
{
    qInstallMessageHandler(messageHandler);
    // Touch the file at startup: an operator who enables the capture after a
    // quiet session still gets a dated file to copy, and the core collector
    // sees the path from the first run.
    writeLine(QStringLiteral("ui start pid=%1").arg(QCoreApplication::applicationPid()));
    writeLine(QStringLiteral("ui log file=%1").arg(logPath));
}
