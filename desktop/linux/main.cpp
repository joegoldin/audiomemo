#include "appearance.h"
#include "wayland.h"

#include <KStatusNotifierItem>
#include <LayerShellQt/Window>
#include <QApplication>
#include <QBackingStore>
#include <QDateTime>
#include <QDBusConnection>
#include <QDBusPendingCallWatcher>
#include <QDBusPendingReply>
#include <QDBusServiceWatcher>
#include <QDBusVariant>
#include <QDir>
#include <QElapsedTimer>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QMenu>
#include <QPainter>
#include <QPluginLoader>
#include <QScreen>
#include <QSocketNotifier>
#include <QSurfaceFormat>
#include <QTimer>
#include <QWindow>

#include <algorithm>
#include <array>
#include <cerrno>
#include <csignal>
#include <fcntl.h>
#include <memory>
#include <optional>
#include <unistd.h>

namespace {
constexpr qsizetype maxFrame = 1 << 20;
constexpr auto screenSaver = "org.freedesktop.ScreenSaver";
constexpr auto screenSaverPath = "/ScreenSaver";
constexpr auto statusWatcher = "org.kde.StatusNotifierWatcher";
constexpr auto statusPath = "/StatusNotifierWatcher";

QDBusMessage getLockState()
{
    auto request = QDBusMessage::createMethodCall(screenSaver, screenSaverPath, screenSaver, "GetActive");
    request.setAutoStartService(false);
    return request;
}

void send(const QJsonObject &event)
{
    const auto line = QJsonDocument(event).toJson(QJsonDocument::Compact) + '\n';
    // Protocol replies are small, fixed messages. Never log frame contents.
    ssize_t n;
    do {
        n = ::write(STDOUT_FILENO, line.constData(), line.size());
    } while (n < 0 && errno == EINTR);
    if (n != line.size())
        QCoreApplication::exit(1);
}

bool layerPluginAvailable()
{
    for (const auto &path : QCoreApplication::libraryPaths()) {
        const QDir plugins(path + "/wayland-shell-integration");
        for (const auto &file : plugins.entryList(QDir::Files)) {
            const auto meta = QPluginLoader(plugins.filePath(file)).metaData().value("MetaData").toObject();
            if (meta.value("Keys").toArray().contains("layer-shell")) {
                QPluginLoader plugin(plugins.filePath(file));
                return plugin.load();
            }
        }
    }
    return false;
}

using appearance::TrayIcon;
using appearance::indicator;
using appearance::trayFrames;

class Caption final : public QWindow
{
public:
    Caption() : backing_(this)
    {
        setFlags(Qt::Tool | Qt::FramelessWindowHint | Qt::WindowDoesNotAcceptFocus | Qt::WindowTransparentForInput);
        QSurfaceFormat format;
        format.setAlphaBufferSize(8);
        setFormat(format);

        auto *shell = LayerShellQt::Window::get(this);
        shell->setScope("audiomemo-caption");
        shell->setAnchors(LayerShellQt::Window::AnchorBottom);
        shell->setLayer(LayerShellQt::Window::LayerTop);
        shell->setKeyboardInteractivity(LayerShellQt::Window::KeyboardInteractivityNone);
        shell->setActivateOnShow(false);
        shell->setCloseOnDismissed(true);
        // 0 uses the area not reserved by panels, without reserving space itself.
        shell->setExclusiveZone(0);
        shell->setMargins(QMargins(0, 0, 0, 20));
#ifdef LAYER_SHELL_EXPLICIT_ACTIVE_SCREEN
        shell->setWantsToBeOnActiveScreen(true);
#else
        shell->setScreenConfiguration(LayerShellQt::Window::ScreenFromCompositor);
#endif
        // The compositor chooses an output at map time. Size conservatively in
        // logical pixels so the surface fits even the smallest scaled output.
        for (const auto *screen : QGuiApplication::screens())
            maxWidth_ = std::min(maxWidth_, std::max(1, screen->geometry().width() - 40));
        connect(&animation_, &QTimer::timeout, this, [this] { requestUpdate(); });
    }

    void updateCaption(const QString &phase, const QJsonObject &preview, bool split)
    {
        split = split || preview.value("split").toBool();
        if (phase_ == phase && preview_ == preview && split_ == split)
            return;
        if (phase_ != phase) {
            phase_ = phase;
            if (phase == "starting" || phase == "finalizing") {
                clock_.start();
                animation_.start(32);
            } else {
                animation_.stop();
            }
        }
        preview_ = preview;
        split_ = split;
        auto desired = appearance::captionSize(phase_, split_);
        desired.setWidth(std::min(desired.width(), maxWidth_));
        if (requestedSize_ != desired) {
            // LayerShellQt forwards window size changes without remapping the surface.
            requestedSize_ = desired;
            resize(desired);
        }
        requestUpdate();
    }

protected:
    bool event(QEvent *event) override
    {
        if (event->type() == QEvent::UpdateRequest) {
            paint();
            return true;
        }
        return QWindow::event(event);
    }
    void exposeEvent(QExposeEvent *) override { paint(); }
    void resizeEvent(QResizeEvent *) override { paint(); }

private:
    void paint()
    {
        if (!isExposed())
            return;
        const QRect box(QPoint(), size());
        backing_.resize(size());
        backing_.beginPaint(box);
        QPainter p(backing_.paintDevice());
        p.setCompositionMode(QPainter::CompositionMode_Source);
        p.fillRect(box, Qt::transparent);
        p.setCompositionMode(QPainter::CompositionMode_SourceOver);
        appearance::paintCaption(p, box, phase_, preview_, split_, clock_.isValid() ? clock_.elapsed() : 0);
        p.end();
        backing_.endPaint();
        backing_.flush(box);
    }

    QBackingStore backing_;
    QSize requestedSize_;
    int maxWidth_ = appearance::captionSize({}, false).width();
    QTimer animation_;
    QElapsedTimer clock_;
    QString phase_;
    QJsonObject preview_;
    bool split_ = false;
};

class Desktop final : public QObject
{
    Q_OBJECT
public:
    explicit Desktop(bool wayland)
        : wayland_(wayland), plugin_(wayland && layerPluginAvailable()), sessionBus_(QDBusConnection::sessionBus()),
          protocol_([this] { capabilities(); }, [this] { return unlocked(); }),
          input_(STDIN_FILENO, QSocketNotifier::Read)
    {
        setObjectName("AudioMemo");
        // Qt may fall back to X11 (or offscreen) if launched out of session. Do
        // not create interactive or focusable fallback UI on those platforms.
        if (wayland_) {
            item_ = std::make_unique<KStatusNotifierItem>("audiomemo");
            item_->setTitle("AudioMemo");
            item_->setCategory(KStatusNotifierItem::ApplicationStatus);
            item_->setStatus(KStatusNotifierItem::Active);
            item_->setStandardActionsEnabled(false);
            item_->setIsMenu(true);
            auto *menu = new QMenu;
            status_ = menu->addAction("AudioMemo — idle");
            status_->setEnabled(false);
            menu->addSeparator();
            meetingStart_ = action(menu, "Start meeting", "meeting.start", true);
            meetingStop_ = action(menu, "Stop meeting", "meeting.stop");
            dictateStart_ = action(menu, "Start Quick Scribe", "dictate.start", true);
            dictateStop_ = action(menu, "Stop Quick Scribe", "dictate.stop");
            copy_ = action(menu, "Copy last result", "last.copy");
            menu->addSeparator();
            quit_ = action(menu, "Quit", "quit");
            item_->setContextMenu(menu);
        }

        sessionBus_.connect(screenSaver, screenSaverPath, screenSaver, "ActiveChanged", this, SLOT(activeChanged(bool)));
        auto *services = new QDBusServiceWatcher(screenSaver, sessionBus_,
                                                QDBusServiceWatcher::WatchForOwnerChange, this);
        services->addWatchedService(statusWatcher);
        connect(services, &QDBusServiceWatcher::serviceOwnerChanged, this,
                [this](const QString &name, const QString &, const QString &) {
                    if (name == screenSaver) {
                        ++lockEpoch_;
                        lockedKnown_ = false;
                        refresh();
                        probeLock();
                    } else {
                        ++trayEpoch_;
                        tray_ = false;
                        refresh();
                        probeTray();
                    }
                });
        connect(&tick_, &QTimer::timeout, this, [this] {
            probeLock();
            probeTray();
            refresh();
        });
        connect(&trayAnimation_, &QTimer::timeout, this, [this] {
            trayFrame_ = (trayFrame_ + 1) % trayFrames;
            item_->setIconByPixmap(animatedIcons_[trayFrame_]);
        });
        connect(&input_, &QSocketNotifier::activated, this, [this] { read(); });
        refresh();
    }

    void start()
    {
        send({{"type", "ready"}});
        if (wayland_)
            protocol_.start();
        QTimer::singleShot(1000, this, [this] {
            capabilitiesKnown_ = true;
            capabilities();
        });
        probeLock();
        probeTray();
        tick_.start(1000);
    }

private Q_SLOTS:
    void activeChanged(bool locked)
    {
        ++lockEpoch_;
        lockedKnown_ = true;
        locked_ = locked;
        refresh();
    }

private:
    bool unlocked() const { return lockedKnown_ && !locked_; }
    bool previewAvailable() const { return plugin_ && protocol_.layerShell(); }
    bool dictating() const { return capture_ == "dictating"; }

    void capabilities()
    {
        // Give late desktop registrations a chance before reporting degradation.
        if (capabilitiesKnown_) {
            warnOnce(previewAvailable(), noPreview_, "no-preview");
            warnOnce(tray_, noTray_, "no-tray");
            warnOnce(lockedKnown_, noLock_, "no-lock-state");
            warnOnce(protocol_.canCopy(), noClipboard_, "no-clipboard");
        }
        refresh();
    }

    static void warnOnce(bool available, bool &warned, const char *code)
    {
        if (!available && !warned)
            send({{"type", "capability"}, {"code", code}});
        warned = !available;
    }

    void probeLock()
    {
        const auto epoch = lockEpoch_;
        auto *watch = new QDBusPendingCallWatcher(sessionBus_.asyncCall(getLockState(), 800), this);
        connect(watch, &QDBusPendingCallWatcher::finished, this, [this, epoch](QDBusPendingCallWatcher *finished) {
            const QDBusPendingReply<bool> reply = *finished;
            finished->deleteLater();
            if (epoch == lockEpoch_) {
                lockedKnown_ = !reply.isError();
                locked_ = lockedKnown_ && reply.value();
                capabilities();
            }
        });
    }

    void probeTray()
    {
        if (!item_)
            return;
        const auto epoch = trayEpoch_;
        auto request = QDBusMessage::createMethodCall(statusWatcher, statusPath, "org.freedesktop.DBus.Properties", "Get");
        request.setAutoStartService(false);
        request << QString(statusWatcher) << QString("IsStatusNotifierHostRegistered");
        auto *watch = new QDBusPendingCallWatcher(sessionBus_.asyncCall(request, 800), this);
        connect(watch, &QDBusPendingCallWatcher::finished, this, [this, epoch](QDBusPendingCallWatcher *finished) {
            const QDBusPendingReply<QDBusVariant> reply = *finished;
            finished->deleteLater();
            if (epoch == trayEpoch_) {
                tray_ = !reply.isError() && reply.value().variant().toBool();
                capabilities();
            }
        });
    }

    QAction *action(QMenu *menu, const char *label, const char *name, bool start = false)
    {
        auto *entry = menu->addAction(label);
        connect(entry, &QAction::triggered, this, [this, name, start] {
            if (!unlocked() || (start && (!tray_ || capture_ != "idle")))
                return;
            send({{"type", "action"}, {"action", name}});
        });
        return entry;
    }

    static QString elapsed(const QDateTime &from)
    {
        if (!from.isValid())
            return {};
        const auto s = std::max<qint64>(0, from.secsTo(QDateTime::currentDateTimeUtc()));
        return QString(" %1:%2:%3").arg(s / 3600, 2, 10, QLatin1Char('0'))
            .arg((s / 60) % 60, 2, 10, QLatin1Char('0')).arg(s % 60, 2, 10, QLatin1Char('0'));
    }

    void updateTrayIcon(TrayIcon state)
    {
        if (lastIcon_ == state)
            return;
        lastIcon_ = state;
        if (state == TrayIcon::Idle) {
            trayAnimation_.stop();
            item_->setIconByPixmap(indicator(state));
            return;
        }
        if (animatedIcons_[0].isNull()) {
            for (int i = 0; i < trayFrames; ++i)
                animatedIcons_[i] = indicator(state, i);
        }
        trayFrame_ = 0;
        item_->setIconByPixmap(animatedIcons_[trayFrame_]);
        trayAnimation_.start(state == TrayIcon::Recording ? 38 : 65);
    }

    void refresh()
    {
        const bool recording = dictating() || capture_ == "meeting";
        const QString phase = capture_ == "meeting" ? QString("meeting") : quickState_;
        const QString id = capture_ == "meeting" ? session_ : quickSession_;
        if (id != overlaySession_) {
            caption_.reset();
            overlaySession_ = id;
        }
        if (phase != overlayPhase_) {
            overlayPhase_ = phase;
            outcomeClock_.start();
        }
        const bool terminal = phase == "copied" || phase == "ready" || phase == "failed";
        const bool visible = phase == "starting" || phase == "recording" || phase == "meeting" || phase == "finalizing"
            || (terminal && outcomeClock_.elapsed() < 4200);
        if (visible && lockedKnown_ && locked_)
            shown_ = id;
        if (!unlocked() || id.isEmpty() || !visible || !previewAvailable()) {
            caption_.reset();
        } else if (!caption_ && shown_ != id) {
            shown_ = id;
            caption_ = std::make_unique<Caption>();
            caption_->updateCaption(phase, preview_, capture_ != "meeting" && quickSplit_);
            caption_->show();
        } else if (caption_) {
            caption_->updateCaption(phase, preview_, capture_ != "meeting" && quickSplit_);
        }
        if (!item_)
            return;
        updateTrayIcon(recording ? TrayIcon::Recording
            : (quickState_ == "finalizing") ? TrayIcon::Refining : TrayIcon::Idle);
        QString title = "Idle";
        if (dictating())
            title = "Quick Scribe" + elapsed(started_);
        else if (capture_ == "meeting")
            title = "Recording meeting" + elapsed(started_);
        else if (capture_ == "starting")
            title = "Waiting for audio";
        else if (capture_ == "stopping")
            title = "Stopping capture";
        QStringList details;
        if (!job_.isEmpty())
            details << "Background job: " + job_;
        if (warning_)
            details << "Check AudioMemo status";
        if (!previewAvailable())
            details << "Captions unavailable";
        if (!protocol_.canCopy())
            details << "Wayland clipboard unavailable";
        if (!unlocked())
            details << "Desktop actions paused (locked or unknown session state)";
        if (!note_.isEmpty() && noteUntil_ > QDateTime::currentMSecsSinceEpoch())
            details << note_;
        const auto detail = details.join(" · ");
        status_->setText(detail.isEmpty() ? title : title + " — " + detail);
        item_->setToolTipTitle("AudioMemo — " + title);
        item_->setToolTipSubTitle(detail);
        meetingStart_->setEnabled(unlocked() && tray_ && capture_ == "idle");
        dictateStart_->setEnabled(meetingStart_->isEnabled());
        meetingStop_->setEnabled(unlocked() && capture_ == "meeting");
        dictateStop_->setEnabled(unlocked() && dictating());
        copy_->setEnabled(unlocked() && protocol_.canCopy());
        quit_->setEnabled(unlocked());
    }

    void read()
    {
        char chunk[16384];
        const auto n = ::read(STDIN_FILENO, chunk, sizeof chunk);
        if (n == 0) {
            QCoreApplication::quit();
            return;
        }
        if (n < 0) {
            if (errno != EAGAIN && errno != EINTR)
                QCoreApplication::exit(1);
            return;
        }
        buffer_.append(chunk, n);
        qsizetype start = 0;
        for (auto nl = buffer_.indexOf('\n'); nl >= 0; nl = buffer_.indexOf('\n', start)) {
            if (nl - start >= maxFrame || !message(QByteArray::fromRawData(buffer_.constData() + start, nl - start))) {
                QCoreApplication::exit(1);
                return;
            }
            start = nl + 1;
        }
        buffer_.remove(0, start);
        if (buffer_.size() >= maxFrame)
            QCoreApplication::exit(1);
    }

    bool message(const QByteArray &frame)
    {
        const auto m = QJsonDocument::fromJson(frame).object();
        const auto type = m.value("type").toString();
        if (type == "view") {
            const auto state = m.value("status").toObject();
            const auto session = state.value("session").toString();
            session_ = session;
            quickSession_ = state.value("quick_session").toString();
            quickState_ = state.value("quick_state").toString();
            capture_ = state.value("capture").toString();
            started_ = QDateTime::fromString(state.value("started_utc").toString(), Qt::ISODateWithMs);
            const auto job = state.value("job_state").toString();
            job_ = QStringList{"queued", "running", "ready", "failed", "canceled"}.contains(job) ? job : QString();
            warning_ = state.value("warning").toBool();
            quickSplit_ = state.value("quick_split").toBool();
            const auto p = m.value("preview").toObject();
            const auto id = capture_ == "meeting" ? session_ : quickSession_;
            preview_ = !id.isEmpty() && p.value("session").toString() == id ? p : QJsonObject{};
            refresh();
        } else if (type == "copy") {
            const auto id = m.value("id").toInteger();
            const auto deadline = m.value("deadline_ms").toInteger();
            const auto text = m.value("text").toString();
            if (id <= 0 || text.isEmpty())
                return false;
            copy(id, deadline, text);
        } else if (type == "note") {
            note_ = m.value("text").toString();
            noteUntil_ = QDateTime::currentMSecsSinceEpoch() + 6000;
            refresh();
        } else {
            return false;
        }
        return true;
    }

    void copy(qint64 id, qint64 deadline, QString text)
    {
        auto reply = [id](const QString &code) { send({{"type", "copied"}, {"id", id}, {"code", code}}); };
        if (!protocol_.canCopy()) {
            reply("unavailable");
            return;
        }
        if (!unlocked()) {
            reply("locked");
            return;
        }
        // Recheck just before committing, not when Go enqueues the UI request.
        auto *watch = new QDBusPendingCallWatcher(sessionBus_.asyncCall(getLockState(), 800), this);
        connect(watch, &QDBusPendingCallWatcher::finished, this,
                [this, deadline, text = std::move(text), reply](QDBusPendingCallWatcher *finished) {
                    const QDBusPendingReply<bool> state = *finished;
                    finished->deleteLater();
                    if (state.isError() || state.value() || !unlocked()) {
                        reply("locked");
                    } else if (deadline <= QDateTime::currentMSecsSinceEpoch()) {
                        reply("expired");
                    } else if (!protocol_.copy(text.toUtf8(), [this, reply](bool ok) {
                                   reply(ok ? "" : unlocked() ? "unavailable" : "locked");
                               })) {
                        reply("unavailable");
                    }
                });
    }

    bool wayland_;
    bool plugin_;
    QDBusConnection sessionBus_;
    Wayland protocol_;
    QSocketNotifier input_;
    QByteArray buffer_;
    std::unique_ptr<KStatusNotifierItem> item_;
    std::unique_ptr<Caption> caption_;
    QAction *status_ = nullptr;
    QAction *meetingStart_ = nullptr;
    QAction *meetingStop_ = nullptr;
    QAction *dictateStart_ = nullptr;
    QAction *dictateStop_ = nullptr;
    QAction *copy_ = nullptr;
    QAction *quit_ = nullptr;
    QTimer tick_;
    QString capture_ = "idle";
    QString session_, shown_, job_, note_, quickSession_, quickState_, overlaySession_, overlayPhase_;
    QElapsedTimer outcomeClock_;
    QDateTime started_;
    qint64 noteUntil_ = 0;
    std::optional<TrayIcon> lastIcon_;
    QTimer trayAnimation_;
    std::array<QIcon, trayFrames> animatedIcons_;
    int trayFrame_ = 0;
    uint64_t lockEpoch_ = 0;
    uint64_t trayEpoch_ = 0;
    bool lockedKnown_ = false;
    bool locked_ = true;
    bool tray_ = false;
    bool warning_ = false;
    bool quickSplit_ = false;
    QJsonObject preview_;
    bool capabilitiesKnown_ = false;
    bool noPreview_ = false;
    bool noTray_ = false;
    bool noLock_ = false;
    bool noClipboard_ = false;
};
}

int main(int argc, char **argv)
{
    std::signal(SIGPIPE, SIG_IGN);
    if (fcntl(STDIN_FILENO, F_SETFL, O_NONBLOCK) < 0 || fcntl(STDOUT_FILENO, F_SETFL, O_NONBLOCK) < 0)
        return 1;
    if (!qEnvironmentVariableIsEmpty("WAYLAND_DISPLAY"))
        qputenv("QT_QPA_PLATFORM", "wayland");
    qputenv("QT_WAYLAND_SHELL_INTEGRATION", "layer-shell");
    QApplication app(argc, argv);
    app.setApplicationName("AudioMemo");
    app.setQuitOnLastWindowClosed(false);
    Desktop desktop(app.platformName().startsWith("wayland"));
    QTimer::singleShot(0, &desktop, [&desktop] { desktop.start(); });
    return app.exec();
}

#include "main.moc"
