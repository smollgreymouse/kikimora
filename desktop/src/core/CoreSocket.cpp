#include "core/CoreSocket.h"

#include <QFile>

#ifdef Q_OS_WIN
#include <QSocketNotifier>

#include <winsock2.h>
#include <ws2tcpip.h>

#if __has_include(<afunix.h>)
#include <afunix.h>
#else
struct sockaddr_un {
    ADDRESS_FAMILY sun_family;
    char sun_path[108];
};
#endif

namespace {

void ensureWinsock()
{
    static const bool ready = [] {
        WSADATA data;
        return WSAStartup(MAKEWORD(2, 2), &data) == 0;
    }();
    Q_UNUSED(ready);
}

} // namespace

CoreSocket::CoreSocket(QObject *parent)
    : QObject(parent)
{
    ensureWinsock();
}

CoreSocket::~CoreSocket()
{
    abort();
}

bool CoreSocket::connectToServer(const QString &path)
{
    abort();
    const SOCKET handle = ::socket(AF_UNIX, SOCK_STREAM, 0);
    if (handle == INVALID_SOCKET) {
        emit errorOccurred();
        return false;
    }

    sockaddr_un address{};
    address.sun_family = AF_UNIX;
    const QByteArray encoded = QFile::encodeName(path);
    if (encoded.isEmpty() || encoded.size() >= int(sizeof(address.sun_path))) {
        ::closesocket(handle);
        emit errorOccurred();
        return false;
    }
    memcpy(address.sun_path, encoded.constData(), size_t(encoded.size()) + 1);

    if (::connect(handle, reinterpret_cast<sockaddr *>(&address), sizeof(address)) == SOCKET_ERROR) {
        ::closesocket(handle);
        m_state = QLocalSocket::UnconnectedState;
        emit errorOccurred();
        return false;
    }

    m_handle = quintptr(handle);
    m_state = QLocalSocket::ConnectedState;
    m_notifier = new QSocketNotifier(qintptr(handle), QSocketNotifier::Read, this);
    connect(m_notifier, &QSocketNotifier::activated, this, &CoreSocket::drain);
    emit connected();
    return true;
}

bool CoreSocket::waitForConnected(int timeoutMs)
{
    Q_UNUSED(timeoutMs);
    // connect() above is synchronous, so the state is already final.
    return m_state == QLocalSocket::ConnectedState;
}

QLocalSocket::LocalSocketState CoreSocket::state() const
{
    return m_state;
}

qint64 CoreSocket::write(const QByteArray &data)
{
    if (m_state != QLocalSocket::ConnectedState) return -1;
    const SOCKET handle = SOCKET(m_handle);
    qint64 sent = 0;
    while (sent < data.size()) {
        const int chunk = ::send(handle, data.constData() + sent, int(data.size() - sent), 0);
        if (chunk <= 0) break;
        sent += chunk;
    }
    return sent;
}

QByteArray CoreSocket::readAll()
{
    QByteArray out;
    out.swap(m_incoming);
    return out;
}

void CoreSocket::drain()
{
    char buffer[64 * 1024];
    for (;;) {
        const int received = ::recv(SOCKET(m_handle), buffer, sizeof(buffer), 0);
        if (received > 0) {
            m_incoming.append(buffer, received);
            emit readyRead();
            continue;
        }
        if (received == 0) {
            const bool wasConnected = m_state == QLocalSocket::ConnectedState;
            abort();
            if (wasConnected) emit disconnected();
            return;
        }
        const int error = WSAGetLastError();
        if (error == WSAEWOULDBLOCK) return;
        const bool wasConnected = m_state == QLocalSocket::ConnectedState;
        abort();
        if (wasConnected) emit disconnected();
        return;
    }
}

void CoreSocket::abort()
{
    if (m_notifier) {
        m_notifier->setEnabled(false);
        m_notifier->deleteLater();
        m_notifier = nullptr;
    }
    if (m_handle != ~quintptr(0)) {
        ::closesocket(SOCKET(m_handle));
        m_handle = ~quintptr(0);
    }
    m_state = QLocalSocket::UnconnectedState;
}

#else // Unix: QLocalSocket is a thin forwarder.

CoreSocket::CoreSocket(QObject *parent)
    : QObject(parent)
    , m_socket(new QLocalSocket(this))
{
    connect(m_socket, &QLocalSocket::connected, this, &CoreSocket::connected);
    connect(m_socket, &QLocalSocket::readyRead, this, [this] {
        m_incoming += m_socket->readAll();
        emit readyRead();
    });
    connect(m_socket, &QLocalSocket::disconnected, this, &CoreSocket::disconnected);
    connect(m_socket, &QLocalSocket::errorOccurred, this, &CoreSocket::errorOccurred);
}

CoreSocket::~CoreSocket() = default;

bool CoreSocket::connectToServer(const QString &path)
{
    m_socket->connectToServer(path);
    return true;
}

bool CoreSocket::waitForConnected(int timeoutMs)
{
    return m_socket->waitForConnected(timeoutMs);
}

QLocalSocket::LocalSocketState CoreSocket::state() const
{
    return m_socket->state();
}

qint64 CoreSocket::write(const QByteArray &data)
{
    return m_socket->write(data);
}

QByteArray CoreSocket::readAll()
{
    QByteArray out;
    out.swap(m_incoming);
    return out;
}

void CoreSocket::abort()
{
    m_socket->abort();
}

#endif
