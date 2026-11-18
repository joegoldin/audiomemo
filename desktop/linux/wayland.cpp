#include "wayland.h"

#include <QSocketNotifier>

#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <unistd.h>
#include <utility>

namespace {
constexpr auto utf8 = "text/plain;charset=utf-8";
constexpr auto plain = "text/plain";

// Clipboard consumers control when their pipe drains. Never block the UI or
// let a consumer accumulate unbounded open pipes.
class Transfer final : public QObject
{
public:
    Transfer(int fd, QByteArray data, QObject *parent)
        : QObject(parent), fd_(fd), data_(std::move(data)), writable_(fd, QSocketNotifier::Write)
    {
        connect(&writable_, &QSocketNotifier::activated, this, [this] {
            const auto n = ::write(fd_, data_.constData() + offset_, data_.size() - offset_);
            if (n > 0) {
                offset_ += n;
                if (offset_ < data_.size())
                    return;
            } else if (n < 0 && (errno == EAGAIN || errno == EINTR)) {
                return;
            }
            writable_.setEnabled(false);
            deleteLater();
        });
    }

    ~Transfer() override { ::close(fd_); }

private:
    int fd_;
    QByteArray data_;
    qsizetype offset_ = 0;
    QSocketNotifier writable_;
};
}

Wayland::Wayland(std::function<void()> changed, std::function<bool()> unlocked)
    : changed_(std::move(changed)), unlocked_(std::move(unlocked))
{
}

Wayland::~Wayland()
{
    done_ = {};
    closeConnection();
}

void Wayland::start()
{
    display_ = wl_display_connect(nullptr);
    if (!display_) {
        changed_();
        return;
    }
    const int fd = wl_display_get_fd(display_);
    read_ = new QSocketNotifier(fd, QSocketNotifier::Read, this);
    write_ = new QSocketNotifier(fd, QSocketNotifier::Write, this);
    write_->setEnabled(false);
    connect(read_, &QSocketNotifier::activated, this, [this] {
        if (wl_display_dispatch(display_) < 0) {
            closeConnection();
            changed_();
        } else {
            flush();
        }
    });
    connect(write_, &QSocketNotifier::activated, this, [this] { flush(); });

    static constexpr wl_registry_listener registryEvents = {
        [](void *self, wl_registry *, uint32_t name, const char *interface, uint32_t) {
            static_cast<Wayland *>(self)->offer(name, interface);
        },
        [](void *self, wl_registry *, uint32_t name) { static_cast<Wayland *>(self)->remove(name); },
    };
    registry_ = wl_display_get_registry(display_);
    wl_registry_add_listener(registry_, &registryEvents, this);
    static constexpr wl_callback_listener discoveryEvents = {
        [](void *self, wl_callback *callback, uint32_t) {
            auto *w = static_cast<Wayland *>(self);
            wl_callback_destroy(callback);
            w->discovery_ = nullptr;
            w->discovered_ = true;
            w->bindSeat();
            w->changed_();
        },
    };
    discovery_ = wl_display_sync(display_);
    wl_callback_add_listener(discovery_, &discoveryEvents, this);
    flush();
}

void Wayland::flush()
{
    if (!display_)
        return;
    if (wl_display_flush(display_) >= 0) {
        write_->setEnabled(false);
    } else if (errno == EAGAIN) {
        write_->setEnabled(true);
    } else {
        closeConnection();
        changed_();
    }
}

void Wayland::offer(uint32_t name, const char *interface)
{
    if (strcmp(interface, "zwlr_layer_shell_v1") == 0) {
        layer_ = name;
    } else if (strcmp(interface, wl_seat_interface.name) == 0) {
        seats_.insert(name, true);
    } else if (strcmp(interface, ext_data_control_manager_v1_interface.name) == 0 && !manager_) {
        manager_ = static_cast<ext_data_control_manager_v1 *>(
            wl_registry_bind(registry_, name, &ext_data_control_manager_v1_interface, 1));
        managerName_ = name;
    }
    if (discovered_) {
        bindSeat();
        changed_();
    }
}

void Wayland::remove(uint32_t name)
{
    if (layer_ == name)
        layer_ = 0;
    if (managerName_ == name) {
        selectionLost();
        ext_data_control_manager_v1_destroy(manager_);
        manager_ = nullptr;
        managerName_ = 0;
    }
    seats_.remove(name);
    if (discovered_) {
        bindSeat();
        changed_();
    }
}

void Wayland::bindSeat()
{
    if (seats_.size() != 1 || !manager_) {
        selectionLost();
        return;
    }
    const uint32_t name = seats_.constBegin().key();
    if (seatName_ == name)
        return;
    selectionLost();
    seat_ = static_cast<wl_seat *>(wl_registry_bind(registry_, name, &wl_seat_interface, 1));
    static constexpr wl_seat_listener seatEvents = {
        [](void *, wl_seat *, uint32_t) {},
        [](void *, wl_seat *, const char *) {},
    };
    wl_seat_add_listener(seat_, &seatEvents, this);
    seatName_ = name;
    device_ = ext_data_control_manager_v1_get_data_device(manager_, seat_);
    static constexpr ext_data_control_device_v1_listener deviceEvents = {
        // This client only writes the clipboard and never requests any offers.
        [](void *, ext_data_control_device_v1 *, ext_data_control_offer_v1 *offer) {
            ext_data_control_offer_v1_destroy(offer);
        },
        [](void *, ext_data_control_device_v1 *, ext_data_control_offer_v1 *) {},
        [](void *self, ext_data_control_device_v1 *) {
            auto *w = static_cast<Wayland *>(self);
            // Keep the seat marker so revocation does not trigger an instant rebind.
            ext_data_control_device_v1_destroy(w->device_);
            w->device_ = nullptr;
            w->finishCopy();
            w->changed_();
        },
        [](void *, ext_data_control_device_v1 *, ext_data_control_offer_v1 *) {},
    };
    ext_data_control_device_v1_add_listener(device_, &deviceEvents, this);
}

bool Wayland::copy(QByteArray text, std::function<void(bool)> done)
{
    if (!canCopy() || !unlocked_() || commit_ || sources_.size() >= 4)
        return false;
    pending_ = ext_data_control_manager_v1_create_data_source(manager_);
    sources_.insert(pending_, std::move(text));
    static constexpr ext_data_control_source_v1_listener sourceEvents = {
        [](void *self, ext_data_control_source_v1 *source, const char *mime, int fd) {
            static_cast<Wayland *>(self)->send(source, mime, fd);
        },
        [](void *self, ext_data_control_source_v1 *source) {
            auto *w = static_cast<Wayland *>(self);
            w->sources_.remove(source);
            ext_data_control_source_v1_destroy(source);
        },
    };
    ext_data_control_source_v1_add_listener(pending_, &sourceEvents, this);
    ext_data_control_source_v1_offer(pending_, utf8);
    ext_data_control_source_v1_offer(pending_, plain);
    ext_data_control_device_v1_set_selection(device_, pending_);
    // Wait for the compositor to process set_selection, rather than acking a write
    // to the Wayland socket. No subsequent delivery overtakes this one.
    static constexpr wl_callback_listener commitEvents = {
        [](void *self, wl_callback *, uint32_t) { static_cast<Wayland *>(self)->finishCopy(); },
    };
    done_ = std::move(done);
    commit_ = wl_display_sync(display_);
    wl_callback_add_listener(commit_, &commitEvents, this);
    flush();
    return true;
}

void Wayland::send(ext_data_control_source_v1 *source, const char *mime, int fd)
{
    const auto text = sources_.constFind(source);
    if (text == sources_.cend() || !canCopy() || !unlocked_() || transfers_ >= 16 ||
        (strcmp(mime, utf8) != 0 && strcmp(mime, plain) != 0) ||
        fcntl(fd, F_SETFL, O_NONBLOCK) < 0) {
        ::close(fd);
        return;
    }
    auto *transfer = new Transfer(fd, *text, this);
    ++transfers_;
    connect(transfer, &QObject::destroyed, this, [this] { --transfers_; });
}

void Wayland::finishCopy()
{
    if (!commit_)
        return;
    wl_callback_destroy(commit_);
    commit_ = nullptr;
    const bool ok = device_ && sources_.contains(pending_) && unlocked_();
    pending_ = nullptr;
    auto done = std::exchange(done_, {});
    if (done)
        done(ok);
}

void Wayland::selectionLost()
{
    if (device_) {
        ext_data_control_device_v1_destroy(device_);
        device_ = nullptr;
    }
    if (seat_) {
        wl_seat_destroy(seat_);
        seat_ = nullptr;
    }
    seatName_ = 0;
    finishCopy();
}

void Wayland::closeConnection()
{
    if (!display_)
        return;
    read_->setEnabled(false);
    write_->setEnabled(false);
    selectionLost();
    for (auto *source : sources_.keys())
        ext_data_control_source_v1_destroy(source);
    sources_.clear();
    if (manager_)
        ext_data_control_manager_v1_destroy(manager_);
    if (discovery_)
        wl_callback_destroy(discovery_);
    wl_registry_destroy(registry_);
    wl_display_disconnect(display_);
    display_ = nullptr;
}
