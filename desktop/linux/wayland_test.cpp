#include "wayland.h"
#include "ext-data-control-server.h"

#include <QCoreApplication>
#include <QEventLoop>
#include <QSocketNotifier>
#include <QTimer>

#include <cstdio>
#include <cstdlib>
#include <fcntl.h>
#include <sys/socket.h>
#include <unistd.h>

namespace {
void check(bool ok, const char *why)
{
    if (!ok) {
        std::fprintf(stderr, "%s\n", why);
        std::exit(1);
    }
}

class Server
{
public:
    Server()
    {
        check(display != nullptr, "create synthetic compositor");
        int fds[2];
        check(socketpair(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0, fds) == 0, "socketpair");
        check(wl_client_create(display, fds[0]) != nullptr, "create synthetic client");
        qputenv("WAYLAND_SOCKET", QByteArray::number(fds[1]));
        wl_global_create(display, &ext_data_control_manager_v1_interface, 1, this, bindManager);
        wl_global_create(display, &wl_seat_interface, 1, this,
                         [](wl_client *client, void *, uint32_t, uint32_t id) {
                             auto *r = wl_resource_create(client, &wl_seat_interface, 1, id);
                             wl_seat_send_capabilities(r, WL_SEAT_CAPABILITY_KEYBOARD);
                         });
        loop = wl_display_get_event_loop(display);
        socket = new QSocketNotifier(wl_event_loop_get_fd(loop), QSocketNotifier::Read);
        QObject::connect(socket, &QSocketNotifier::activated, socket, [this] {
            check(wl_event_loop_dispatch(loop, 0) == 0, "dispatch synthetic compositor events");
            wl_display_flush_clients(display);
        });
    }
    ~Server()
    {
        delete socket;
        wl_display_destroy_clients(display);
        wl_display_destroy(display);
    }

    wl_display *display = wl_display_create();
    wl_event_loop *loop;
    QSocketNotifier *socket;
    wl_resource *device = nullptr;
    wl_resource *selection = nullptr;
    int changes = 0;

private:
    static void bindManager(wl_client *client, void *data, uint32_t, uint32_t id)
    {
        static constexpr struct ext_data_control_manager_v1_interface managerRequests = {
            [](wl_client *client, wl_resource *, uint32_t id) {
                static constexpr struct ext_data_control_source_v1_interface sourceRequests = {
                    [](wl_client *, wl_resource *, const char *) {},
                    destroy,
                };
                auto *r = wl_resource_create(client, &ext_data_control_source_v1_interface, 1, id);
                wl_resource_set_implementation(r, &sourceRequests, nullptr, nullptr);
            },
            [](wl_client *client, wl_resource *manager, uint32_t id, wl_resource *) {
                static constexpr struct ext_data_control_device_v1_interface deviceRequests = {
                    [](wl_client *, wl_resource *r, wl_resource *source) {
                        auto *server = static_cast<Server *>(wl_resource_get_user_data(r));
                        if (server->selection)
                            ext_data_control_source_v1_send_cancelled(server->selection);
                        server->selection = source;
                        ++server->changes;
                    },
                    destroy,
                    [](wl_client *, wl_resource *, wl_resource *) { check(false, "changed primary selection"); },
                };
                auto *server = static_cast<Server *>(wl_resource_get_user_data(manager));
                auto *r = wl_resource_create(client, &ext_data_control_device_v1_interface, 1, id);
                wl_resource_set_implementation(r, &deviceRequests, server, nullptr);
                server->device = r;
                ext_data_control_device_v1_send_selection(r, nullptr);
            },
            destroy,
        };
        auto *manager = wl_resource_create(client, &ext_data_control_manager_v1_interface, 1, id);
        wl_resource_set_implementation(manager, &managerRequests, data, nullptr);
    }

    static void destroy(wl_client *, wl_resource *r) { wl_resource_destroy(r); }
};

void until(const std::function<bool()> &ready, const char *why)
{
    QEventLoop loop;
    QTimer poll, timeout;
    QObject::connect(&poll, &QTimer::timeout, &loop, [&] { if (ready()) loop.quit(); });
    QObject::connect(&timeout, &QTimer::timeout, &loop, &QEventLoop::quit);
    poll.start(1);
    timeout.start(2000);
    if (!ready())
        loop.exec();
    check(ready(), why);
}
}

int main(int argc, char **argv)
{
    QCoreApplication app(argc, argv);
    Server server;
    bool unlocked = true;
    Wayland clipboard([] {}, [&] { return unlocked; });
    clipboard.start();
    until([&] { return clipboard.canCopy() && server.device; }, "discover data-control seat");

    const auto copy = [&](QByteArray text) {
        bool ack = false;
        check(clipboard.copy(text, [&](bool ok) { check(ok, "clipboard acknowledgement"); ack = true; }), "queue clipboard copy");
        until([&] { return ack; }, "wait for compositor acknowledgement");
    };
    copy("first");
    const QByteArray final = QByteArray(32768, 'a') + "\nUTF-8: £";
    copy(final);
    check(server.changes == 2, "each copy should select only once");

    int pipe[2];
    check(pipe2(pipe, O_CLOEXEC | O_NONBLOCK) == 0, "create synthetic paste pipe");
    ext_data_control_source_v1_send_send(server.selection, "text/plain;charset=utf-8", pipe[1]);
    close(pipe[1]);
    wl_display_flush_clients(server.display);
    QByteArray result;
    until([&] {
        char piece[3333];
        const auto n = read(pipe[0], piece, sizeof(piece));
        if (n > 0)
            result.append(piece, n);
        return n == 0;
    }, "close clipboard paste pipe after writing");
    close(pipe[0]);
    check(result == final, "paste consumer receives the replaced selection byte for byte");

    unlocked = false;
    check(!clipboard.copy("locked", [](bool) {}), "deny copy while locked");
    check(server.changes == 2, "locked copy must not change selection");
    unlocked = true;
    ext_data_control_device_v1_send_finished(server.device);
    wl_display_flush_clients(server.display);
    until([&] { return !clipboard.canCopy(); }, "observe data-control revocation");
    check(!clipboard.copy("revoked", [](bool) {}), "deny copy after revocation");
}
