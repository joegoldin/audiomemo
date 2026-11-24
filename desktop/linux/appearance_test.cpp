#include "appearance.h"

#include <QApplication>
#include <QDebug>
#include <QDir>

#include <algorithm>

namespace {
using appearance::TrayIcon;
using appearance::trayFrames;

bool waveform()
{
    int shortest[] = {22, 22, 22}, tallest[] = {0, 0, 0};
    int previous[] = {0, 0, 0}, first[] = {0, 0, 0};
    for (int frame = 0; frame < trayFrames; ++frame) {
        const auto image = appearance::indicator(TrayIcon::Recording, frame).pixmap(22, 22).toImage();
        for (int bar = 0; bar < 3; ++bar) {
            int top = 22, bottom = 0;
            for (int y = 0; y < 22; ++y) {
                if (image.pixelColor(7 + 4.5 * bar, y).alpha() >= 128) {
                    top = std::min(top, y);
                    bottom = y;
                }
            }
            const int height = bottom - top + 1;
            if (std::abs(top + bottom - 21) > 1 || (frame && std::abs(previous[bar] - height) > 3))
                return false;
            if (!frame)
                first[bar] = height;
            previous[bar] = height;
            shortest[bar] = std::min(shortest[bar], height);
            tallest[bar] = std::max(tallest[bar], height);
        }
        // No detached recording dot or sideways motion.
        for (int y = 0; y < 22; ++y) {
            if (image.pixelColor(19, y).alpha() != 0 || image.pixelColor(3, y).alpha() != 0)
                return false;
        }
    }
    for (int bar = 0; bar < 3; ++bar) {
        if (tallest[bar] - shortest[bar] < 8 || std::abs(first[bar] - previous[bar]) > 3)
            return false;
    }
    return first[0] != first[1];
}

QImage caption(const QString &phase, const QJsonObject &preview, bool split)
{
    QImage image(appearance::captionSize(phase, split), QImage::Format_ARGB32_Premultiplied);
    image.fill(Qt::transparent);
    QPainter p(&image);
    appearance::paintCaption(p, image.rect(), phase, preview, split, 0);
    p.end();
    return image;
}

bool captions(const QString &output)
{
    const QJsonObject them{{"stable", "Let's review the revised design tomorrow."},
                           {"tentative", "Does ten still work for you?"}, {"level", 0.7}};
    const QJsonObject us{{"stable", "Ten works. I'll send over the new version today."},
                         {"tentative", "It has the updated spacing…"}, {"level", 0.4}};
    const QJsonObject preview{{"us", us}, {"them", them}};
    const auto single = caption("recording", us, false);
    const auto split = caption("recording", preview, true);
    const auto opening = caption("starting", {}, true);
    const auto copied = caption("copied", preview, true);
    const auto noUs = caption("recording", {{"them", them}}, true);
    const auto noThem = caption("recording", {{"us", us}}, true);
    const auto refined = caption("finalizing", preview, true);
    const auto meeting = caption("meeting", preview, true);
    const auto meetingNoThem = caption("meeting", {{"us", us}}, true);
    const auto singleMeeting = caption("meeting", us, false);
    if (!output.isEmpty()) {
        const QDir dir(output);
        if (!single.save(dir.filePath("caption-single.png")) || !split.save(dir.filePath("caption-stacked.png")) ||
            !refined.save(dir.filePath("caption-refining.png")))
            return false;
    }
    if (split.size() != QSize(560, 218) || opening.size() != split.size() || refined.size() != split.size() ||
        meeting.size() != split.size() || single.size() != QSize(720, 104) ||
        singleMeeting.size() != single.size() || copied.size() != single.size())
        return false;
    const QRect top(0, 0, 560, 104), bottom(0, 114, 560, 104);
    return split.pixelColor(280, 109).alpha() == 0 && opening.pixelColor(280, 109).alpha() == 0 &&
        refined.pixelColor(280, 109).alpha() == 0 && meeting.pixelColor(280, 109).alpha() == 0 &&
        meeting.copy(top) != meetingNoThem.copy(top) && meeting.copy(bottom) == meetingNoThem.copy(bottom) &&
        split.pixelColor(1, 52).alpha() > 230 && split.pixelColor(558, 52).alpha() > 230 &&
        split.pixelColor(1, 166).alpha() > 230 && split.pixelColor(558, 166).alpha() > 230 &&
        split.copy(top) == noUs.copy(top) && split.copy(bottom) != noUs.copy(bottom) &&
        split.copy(top) != noThem.copy(top) && split.copy(bottom) == noThem.copy(bottom) &&
        single.pixelColor(360, 52).alpha() > 230 && copied.pixelColor(360, 20).alpha() == 0 &&
        copied.pixelColor(360, 80).alpha() > 230;
}
}

int main(int argc, char **argv)
{
    QApplication app(argc, argv);
    if (!waveform()) {
        qCritical("Recording icon lost its centered, phased waveform");
        return 1;
    }
    if (!captions(app.arguments().value(1))) {
        qCritical("Caption rendering lost its stacked lanes or compact result state");
        return 1;
    }
}
