#include "core/RealCoreClient.h"

#include <QDir>
#include <QElapsedTimer>
#include <QFile>
#include <QFileInfo>
#include <QProcess>
#include <QTemporaryDir>
#include <QTest>

namespace {
bool writeText(const QString &path, const QString &text)
{
    QFile file(path);
    if (!file.open(QIODevice::WriteOnly | QIODevice::Truncate)) return false;
    return file.write(text.toUtf8()) == text.toUtf8().size();
}

QString makeConfig(const QString &root, const QString &name, const QString &protocol)
{
    const QString stateDir = QDir(root).filePath(name + QStringLiteral("-state"));
    QString sections;
    if (protocol == QStringLiteral("amneziawg2")) {
        sections = QStringLiteral("\n[awg2]\n"
                                  "private_key = \"test-private-key\"\n"
                                  "peer_public_key = \"test-peer-key\"\n"
                                  "endpoint = \"192.0.2.1:51820\"\n"
                                  "allowed_ips = [\"0.0.0.0/0\"]\n"
                                  "jc = 1\n"
                                  "jmin = 2\n"
                                  "jmax = 3\n");
    } else if (protocol == QStringLiteral("vless-reality")) {
        sections = QStringLiteral("\n[vless_reality]\n"
                                  "endpoint = \"192.0.2.2:443\"\n"
                                  "uuid = \"11111111-1111-1111-1111-111111111111\"\n"
                                  "server_name = \"example.com\"\n"
                                  "public_key = \"test-public-key\"\n"
                                  "transport = \"raw\"\n");
    } else {
        const QString passwordFile = QDir(root).filePath(name + QStringLiteral("-password"));
        if (!writeText(passwordFile, QStringLiteral("fixture-secret\n"))) return {};
        sections = QStringLiteral("\n[openconnect]\n"
                                  "gateway = \"vpn.example.test\"\n"
                                  "username = \"fixture-user\"\n"
                                  "password_file = \"") + passwordFile + QStringLiteral("\"\n"
                                  "vpn_protocol = \"anyconnect\"\n"
                                  "token_mode = \"none\"\n");
    }
    const QString path = QDir(root).filePath(name + QStringLiteral(".toml"));
    const QString config = QStringLiteral("name = \"") + name + QStringLiteral("\"\nprotocol = \"") +
                           protocol + QStringLiteral("\"\ninterface = \"kk-") + name +
                           QStringLiteral("0\"\nmtu = 1380\nstate_dir = \"") + stateDir +
                           QStringLiteral("\"\naddress = [\"10.0.0.2/32\"]\n") + sections;
    return writeText(path, config) ? path : QString{};
}

bool allRoles(RealCoreClient &client, const QString &state)
{
    for (int row = 0; row < client.roles()->rowCount(); ++row) {
        const RoleSnapshot role = client.roles()->roleAt(row);
        if (role.state != state) return false;
        if (state == QStringLiteral("Ready") && !role.sessionConnected) return false;
    }
    return client.roles()->rowCount() == 3;
}
}

class RealCoreEndToEndTest final : public QObject
{
    Q_OBJECT

private slots:
    void uiCoreAndFakeToadsLifecycle()
    {
        const QString coreBinary = qEnvironmentVariable("KIKIMORA_CORE_BINARY");
        const QString fakeToadBinary = qEnvironmentVariable("KIKIMORA_FAKE_TOAD_BINARY");
        if (coreBinary.isEmpty() || fakeToadBinary.isEmpty() || !QFileInfo::exists(coreBinary) ||
            !QFileInfo::exists(fakeToadBinary)) {
            QSKIP("KIKIMORA_CORE_BINARY and KIKIMORA_FAKE_TOAD_BINARY are required");
        }

        QTemporaryDir temp;
        QVERIFY(temp.isValid());
        const QStringList configs{
            makeConfig(temp.path(), QStringLiteral("awg2"), QStringLiteral("amneziawg2")),
            makeConfig(temp.path(), QStringLiteral("vless"), QStringLiteral("vless-reality")),
            makeConfig(temp.path(), QStringLiteral("oc"), QStringLiteral("openconnect"))};
        for (const QString &config : configs) QVERIFY(!config.isEmpty());

        const QString socket = QDir(temp.path()).filePath(QStringLiteral("core.sock"));
        QProcess core;
        core.setProcessChannelMode(QProcess::MergedChannels);
        QStringList args{QStringLiteral("serve"), QStringLiteral("--socket"), socket,
                         QStringLiteral("--toad-binary"), fakeToadBinary};
        for (const QString &config : configs) args << QStringLiteral("--config") << config;
        core.start(coreBinary, args);
        QVERIFY2(core.waitForStarted(2000), qPrintable(core.errorString()));
        struct ProcessGuard {
            QProcess *process;
            ~ProcessGuard()
            {
                if (process->state() != QProcess::NotRunning) {
                    process->terminate();
                    if (!process->waitForFinished(1000)) process->kill();
                }
            }
        } coreGuard{&core};
        QTRY_VERIFY_WITH_TIMEOUT(QFileInfo::exists(socket), 2000);

        RealCoreClient client(socket, nullptr);
        QTRY_VERIFY_WITH_TIMEOUT(client.revision() > 0, 3000);
        const qulonglong initialRevision = client.revision();
        QTRY_VERIFY_WITH_TIMEOUT(client.roles()->rowCount() == 3, 1000);

        client.connectAll();
        QElapsedTimer lifecycleTimer;
        lifecycleTimer.start();
        while (!allRoles(client, QStringLiteral("Ready")) && lifecycleTimer.elapsed() < 4000)
            QTest::qWait(20);
        if (!allRoles(client, QStringLiteral("Ready"))) {
            QString states;
            for (int row = 0; row < client.roles()->rowCount(); ++row) {
                const RoleSnapshot role = client.roles()->roleAt(row);
                states += QStringLiteral("%1=%2 connected=%3; ").arg(role.id, role.state).arg(role.sessionConnected);
            }
            QFAIL(qPrintable(QStringLiteral("roles did not become ready, revision=%1: %2\ncore output: %3")
                                 .arg(client.revision()).arg(states, QString::fromUtf8(core.readAll()))));
        }
        QVERIFY(client.revision() > initialRevision);

        client.disconnectAll();
        QTRY_VERIFY_WITH_TIMEOUT(allRoles(client, QStringLiteral("Stopped")), 4000);

        core.terminate();
        if (!core.waitForFinished(2000)) core.kill();
        QVERIFY2(core.exitStatus() == QProcess::NormalExit, qPrintable(core.readAll()));
    }
};

QTEST_MAIN(RealCoreEndToEndTest)
#include "RealCoreEndToEndTest.moc"
