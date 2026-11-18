#pragma once

#include "ext-data-control.h"

#include <QByteArray>
#include <QHash>
#include <QObject>

#include <functional>

class QSocketNotifier;

// Use a separate, surface-less Wayland connection. Qt's regular clipboard can
// require a focus serial; data-control never needs to create a focusable window.
class Wayland final : public QObject
{
public:
    Wayland(std::function<void()> changed, std::function<bool()> unlocked);
    ~Wayland() override;

    void start();
    bool layerShell() const { return layer_ && display_; }
    bool canCopy() const { return device_ && display_; }
    bool copy(QByteArray text, std::function<void(bool)> done);

private:
    void flush();
    void closeConnection();
    void offer(uint32_t name, const char *interface);
    void remove(uint32_t name);
    void bindSeat();
    void selectionLost();
    void send(ext_data_control_source_v1 *source, const char *mime, int fd);
    void finishCopy();

    std::function<void()> changed_;
    std::function<bool()> unlocked_;
    wl_display *display_ = nullptr;
    wl_registry *registry_ = nullptr;
    wl_callback *discovery_ = nullptr;
    wl_callback *commit_ = nullptr;
    wl_seat *seat_ = nullptr;
    QHash<uint32_t, bool> seats_;
    ext_data_control_manager_v1 *manager_ = nullptr;
    ext_data_control_device_v1 *device_ = nullptr;
    QHash<ext_data_control_source_v1 *, QByteArray> sources_;
    ext_data_control_source_v1 *pending_ = nullptr;
    std::function<void(bool)> done_;
    QSocketNotifier *read_ = nullptr;
    QSocketNotifier *write_ = nullptr;
    uint32_t layer_ = 0;
    uint32_t managerName_ = 0;
    uint32_t seatName_ = 0;
    int transfers_ = 0;
    bool discovered_ = false;
};
