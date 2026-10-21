#pragma once

#include <QByteArray>
#include <QLocalSocket>
#include <QObject>

class QSocketNotifier;

// Transport for the core's local control socket.
//
// On Unix this is QLocalSocket over AF_UNIX. On Windows QLocalSocket only speaks
// named pipes, while the core listens on an AF_UNIX path (Go supports that on
// Windows), so the Windows build talks to the socket through Winsock directly.
// The class deliberately mirrors the subset of QLocalSocket that RealCoreClient
// uses, so the client code stays transport-agnostic.
class CoreSocket final : public QObject
{
    Q_OBJECT

public:
    explicit CoreSocket(QObject *parent = nullptr);
    ~CoreSocket() override;

    bool connectToServer(const QString &path);
    bool waitForConnected(int timeoutMs = 5000);
    QLocalSocket::LocalSocketState state() const;

    qint64 write(const QByteArray &data);
    void flush() {}
    QByteArray readAll();
    void abort();

signals:
    void connected();
    void readyRead();
    void disconnected();
    void errorOccurred();

private:
#ifdef Q_OS_WIN
    void drain();
    quintptr m_handle = ~quintptr(0);
    QSocketNotifier *m_notifier = nullptr;
    QLocalSocket::LocalSocketState m_state = QLocalSocket::UnconnectedState;
#else
    QLocalSocket *m_socket = nullptr;
#endif
    QByteArray m_incoming;
};
