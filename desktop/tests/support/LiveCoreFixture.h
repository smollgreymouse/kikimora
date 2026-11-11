#pragma once

#include <QProcess>
#include <QString>
#include <QTemporaryDir>

// Hermetic real-core fixture for the Layer-C suites (U5, docs/roadmap.md
// §12.6): the test owns the exact kikimora-core process, a fake Toad helper
// and a temp filesystem root carrying every mutable runtime path, so the
// suites never touch product /etc, /var/lib, /run or ProgramData state and
// never depend on an operator-installed core.
namespace UiTest {

class LiveCoreFixture
{
public:
    // Resolves the exact binaries the runner provides. A mandatory Layer-C
    // lane must fail loudly when they are missing: this is a harness failure,
    // never a skip.
    static bool resolveBinaries(QString *coreBinary, QString *toadBinary, QString *error);

    LiveCoreFixture(QString coreBinary, QString toadBinary);
    ~LiveCoreFixture();

    LiveCoreFixture(const LiveCoreFixture &) = delete;
    LiveCoreFixture &operator=(const LiveCoreFixture &) = delete;

    // Writes the deterministic alpha/beta fixture and launches the core with
    // every runtime path under the temp root. Returns false with a reason
    // when the process never came up.
    bool start(QString *error);
    // Graceful stop; records whether the process exited normally.
    void stop();
    // stop() + start(): the same root, a fresh core process. The desktop
    // client reconnects on its own backoff.
    bool restart(QString *error);

    QString socketPath() const { return m_socket; }
    QString root() const { return m_temp.path(); }
    QString publicationDir() const { return m_publication; }
    // Core output for failure messages.
    QString output() { return QString::fromUtf8(m_core.readAllStandardError() + m_core.readAllStandardOutput()); }
    bool exitedNormally() const { return m_exitNormal; }

private:
    QStringList coreArguments() const;

    QString m_coreBinary;
    QString m_toadBinary;
    QTemporaryDir m_temp;
    QProcess m_core;
    QString m_socket;
    QString m_configDir;
    QString m_publication;
    bool m_running = false;
    bool m_exitNormal = true;
};

} // namespace UiTest
