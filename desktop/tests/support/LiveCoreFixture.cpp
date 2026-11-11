#include "LiveCoreFixture.h"

#include <QDir>
#include <QElapsedTimer>
#include <QFile>
#include <QFileInfo>
#include <QTest>
#include <utility>

namespace UiTest {

namespace {
bool writeText(const QString &path, const QString &text)
{
    QFile file(path);
    if (!file.open(QIODevice::WriteOnly | QIODevice::Truncate)) return false;
    return file.write(text.toUtf8()) == text.toUtf8().size();
}

// One deterministic role: openconnect against example.test credentials that
// only exist inside the temp root. The fake Toad helper answers locally, so
// nothing here reaches a real VPN endpoint.
QString roleConfig(const QString &root, const QString &name, const QStringList &profiles)
{
    QStringList quoted;
    quoted.reserve(profiles.size());
    for (const QString &profile : profiles)
        quoted << QStringLiteral("\"") + profile + QStringLiteral("\"");
    const QString passwordFile = QDir(root).filePath(name + QStringLiteral(".password"));
    if (!writeText(passwordFile, QStringLiteral("fixture-secret\n"))) return {};
    const QStringList sections{
        QStringLiteral("name = \"%1\"").arg(name),
        QStringLiteral("protocol = \"openconnect\""),
        QStringLiteral("interface = \"kk-%1\"").arg(name + QStringLiteral("0")),
        QStringLiteral("mtu = 1380"),
        QStringLiteral("state_dir = \"%1\"").arg(QDir(root).filePath(QStringLiteral("state/%1").arg(name))),
        QStringLiteral("address = [\"10.0.0.2/32\"]"),
        QStringLiteral("profiles = [%1]").arg(quoted.join(QStringLiteral(", "))),
        QStringLiteral(""),
        QStringLiteral("[openconnect]"),
        QStringLiteral("gateway = \"vpn.example.test\""),
        QStringLiteral("username = \"fixture-%1\"").arg(name),
        QStringLiteral("password_file = \"%1\"").arg(passwordFile),
        QStringLiteral("vpn_protocol = \"anyconnect\""),
        QStringLiteral("token_mode = \"none\""),
    };
    // root already IS the configs directory (--config-dir scans it).
    const QString path = QDir(root).filePath(QStringLiteral("%1.toml").arg(name));
    return writeText(path, sections.join(QStringLiteral("\n")) + QStringLiteral("\n")) ? path : QString{};
}
} // namespace

bool LiveCoreFixture::resolveBinaries(QString *coreBinary, QString *toadBinary, QString *error)
{
    const QString core = qEnvironmentVariable("KIKIMORA_CORE_BINARY");
    const QString toad = qEnvironmentVariable("KIKIMORA_FAKE_TOAD_BINARY");
    if (core.isEmpty() || toad.isEmpty()) {
        *error = QStringLiteral("Layer C is mandatory: KIKIMORA_CORE_BINARY and "
                                "KIKIMORA_FAKE_TOAD_BINARY must point at the built "
                                "kikimora-core and fake-toad helper (the required-suite "
                                "runner builds both)");
        return false;
    }
    if (!QFileInfo::exists(core) || !QFileInfo::exists(toad)) {
        *error = QStringLiteral("KIKIMORA_CORE_BINARY/KIKIMORA_FAKE_TOAD_BINARY point at missing files: %1 %2")
                     .arg(core, toad);
        return false;
    }
    *coreBinary = core;
    *toadBinary = toad;
    return true;
}

LiveCoreFixture::LiveCoreFixture(QString coreBinary, QString toadBinary)
    : m_coreBinary(std::move(coreBinary))
    , m_toadBinary(std::move(toadBinary))
{
    m_core.setProcessChannelMode(QProcess::SeparateChannels);
}

LiveCoreFixture::~LiveCoreFixture()
{
    stop();
}

QStringList LiveCoreFixture::coreArguments() const
{
    // The exact hermetic serve line (roadmap §12.6): every mutable surface
    // lives under the temp root and diagnostics ambient host sources are off.
    return {
        QStringLiteral("serve"),
        QStringLiteral("--config-dir"), QDir(m_temp.path()).filePath(QStringLiteral("configs")),
        QStringLiteral("--state-dir"), QDir(m_temp.path()).filePath(QStringLiteral("state")),
        QStringLiteral("--leshy-publication-dir"), m_publication,
        QStringLiteral("--leshy-config"), QDir(m_temp.path()).filePath(QStringLiteral("leshy/config.toml")),
        QStringLiteral("--routing-dir"), QDir(m_temp.path()).filePath(QStringLiteral("leshy/domains")),
        QStringLiteral("--bypass-config"), QDir(m_temp.path()).filePath(QStringLiteral("leshy/bypass.json")),
        QStringLiteral("--endpoint-provider-dir"), QDir(m_temp.path()).filePath(QStringLiteral("providers")),
        QStringLiteral("--socket"), m_socket,
        QStringLiteral("--toad-binary"), m_toadBinary,
        QStringLiteral("--diagnostics-host-sources=false"),
        // The owned core must never restart the host's real Leshy unit after
        // routing/bypass writes (the matrix round trips exercise exactly
        // those); the no-op hook stands in for "systemctl restart leshy".
        QStringLiteral("--leshy-reload=false"),
    };
}

bool LiveCoreFixture::start(QString *error)
{
    QElapsedTimer timer;
    timer.start();

    QDir root(m_temp.path());
    if (!root.exists()) {
        *error = QStringLiteral("temp root disappeared");
        return false;
    }
    root.mkpath(QStringLiteral("configs"));
    root.mkpath(QStringLiteral("state"));
    root.mkpath(QStringLiteral("providers"));
    root.mkpath(QStringLiteral("publication"));
    root.mkpath(QStringLiteral("leshy/domains"));
    m_configDir = root.filePath(QStringLiteral("configs"));
    m_publication = root.filePath(QStringLiteral("publication"));
    m_socket = root.filePath(QStringLiteral("core.sock"));

    writeText(root.filePath(QStringLiteral("configs/profiles.list")), QStringLiteral("work\nhome\n"));
    QFile::remove(m_socket);
    const QString alpha = roleConfig(m_configDir, QStringLiteral("alpha"),
                                     {QStringLiteral("default"), QStringLiteral("home")});
    const QString beta = roleConfig(m_configDir, QStringLiteral("beta"),
                                    {QStringLiteral("default"), QStringLiteral("work")});
    if (alpha.isEmpty() || beta.isEmpty()) {
        *error = QStringLiteral("cannot write deterministic role configs (alpha=%1 beta=%2) configs=%3")
                     .arg(alpha, beta, m_configDir);
        return false;
    }
    writeText(root.filePath(QStringLiteral("leshy/domains/alpha.txt")),
              QStringLiteral("# alpha fixture list\nchatgpt.com\n*.openai.com\n"));
    writeText(root.filePath(QStringLiteral("leshy/domains/beta.txt")),
              QStringLiteral("# beta fixture list\nad-tech.ru\n"));
    // The restart marker must stay inside the temp root.
    QProcessEnvironment env = QProcessEnvironment::systemEnvironment();
    env.insert(QStringLiteral("KIKIMORA_RESTART_MARKER"),
               root.filePath(QStringLiteral("core-restart.pending")));
    m_core.setProcessEnvironment(env);

    m_core.start(m_coreBinary, coreArguments());
    if (!m_core.waitForStarted(5000)) {
        *error = QStringLiteral("core did not start: %1 (%2)")
                     .arg(m_core.errorString(), QString::fromUtf8(m_core.readAllStandardError()));
        return false;
    }
    m_running = true;
    // Wait for the control socket: the core binds it in Serve after the
    // managed-authority startup sequence wrote the generated config.
    while (!QFileInfo::exists(m_socket)) {
        if (m_core.state() != QProcess::Running) {
            *error = QStringLiteral("core exited before binding %1: %2")
                         .arg(m_socket, QString::fromUtf8(m_core.readAllStandardError()));
            m_running = false;
            return false;
        }
        if (timer.elapsed() > 20000) {
            *error = QStringLiteral("core never bound %1 within 20s; stderr: %2")
                         .arg(m_socket, QString::fromUtf8(m_core.readAllStandardError()));
            return false;
        }
        QTest::qWait(50);
    }
    // The generated config is written during startup authority; once present,
    // every zone route_target must live under the shared publication root.
    const QString generated = root.filePath(QStringLiteral("leshy/config.toml"));
    while (!QFileInfo::exists(generated)) {
        if (timer.elapsed() > 20000) {
            *error = QStringLiteral("core never wrote %1; stderr: %2")
                         .arg(generated, QString::fromUtf8(m_core.readAllStandardError()));
            return false;
        }
        QTest::qWait(50);
    }
    QFile config(generated);
    if (!config.open(QIODevice::ReadOnly)) {
        *error = QStringLiteral("cannot read %1").arg(generated);
        return false;
    }
    const QStringList lines = QString::fromUtf8(config.readAll()).split(QLatin1Char('\n'));
    bool sawRouteTarget = false;
    for (const QString &line : lines) {
        if (!line.startsWith(QStringLiteral("route_target"))) continue;
        sawRouteTarget = true;
        // route_target = "<path>": the zone must publish under the shared
        // root. The generated config is TOML, so a Windows path arrives as
        // an escaped basic string ("C:\\Users\\..."): unescape it, then
        // normalize both sides to forward slashes.
        QString value = line.section(QLatin1Char('"'), 1, 1);
        value.replace(QLatin1String("\\\\"), QLatin1String("\\"));
        value = QDir::fromNativeSeparators(value);
        const QString root = QDir::fromNativeSeparators(m_publication);
        if (!value.startsWith(root)) {
            *error = QStringLiteral("generated route target escaped the publication root: %1")
                         .arg(value);
            return false;
        }
    }
    if (!sawRouteTarget) {
        // The alpha/beta lists guarantee zones; a config without targets means
        // the fixture data did not reach the generator.
        *error = QStringLiteral("generated config %1 carries no route_target").arg(generated);
        return false;
    }
    return true;
}

void LiveCoreFixture::stop()
{
    if (!m_running)
        return;
    m_running = false;
    m_core.terminate();
    if (!m_core.waitForFinished(5000)) {
        m_core.kill();
        m_core.waitForFinished(2000);
    }
    m_exitNormal = m_core.exitStatus() == QProcess::NormalExit;
}

bool LiveCoreFixture::restart(QString *error)
{
    stop();
    // The restarted local core regenerates its privileged credential: the
    // hardened key DACL (§31.6) grants SYSTEM/Administrators only, so an
    // unelevated same-user core could never read the previous file back.
    // A production restart runs as SYSTEM and reuses the key; this fixture
    // is a local install with no Leshy peer holding the old credential.
    QFile::remove(QDir(m_temp.path()).filePath(QStringLiteral("state/control-route-auth.key")));
    return start(error);
}

} // namespace UiTest
