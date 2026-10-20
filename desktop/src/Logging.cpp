#include "Logging.h"

#include <QDateTime>
#include <QFile>
#include <QMutex>
#include <QTextStream>

namespace {

QMutex logMutex;
QString logPath;
bool logPathResolved = false;
bool logEnabled = false;

void resolvePath()
{
    if (logPathResolved) return;
    logPathResolved = true;
    logPath = qEnvironmentVariable("KIKIMORA_UI_DEBUG_LOG");
    logEnabled = !logPath.isEmpty();
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
    QMutexLocker locker(&logMutex);
    resolvePath();
    if (!logEnabled) return;

    QFile file(logPath);
    if (!file.open(QIODevice::WriteOnly | QIODevice::Append | QIODevice::Text)) return;
    QTextStream stream(&file);
    stream << QDateTime::currentDateTime().toString(Qt::ISODateWithMs) << ' ' << message << '\n';
}
