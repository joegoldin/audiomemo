#include "appearance.h"

#include <QGuiApplication>
#include <QTextBlock>
#include <QTextCursor>
#include <QTextDocument>
#include <QTextLayout>

#include <algorithm>
#include <cmath>

namespace appearance {
QIcon indicator(TrayIcon state, int frame)
{
    static constexpr int still[] = {8, 17, 12};
    QIcon icon;
    for (const int px : {16, 22, 32, 48}) {
        QPixmap image(px, px);
        image.fill(Qt::transparent);
        QPainter p(&image);
        p.setRenderHint(QPainter::Antialiasing);
        p.scale(px / 22.0, px / 22.0);
        // A fine outline keeps the pixmap legible on a panel of either color.
        p.setPen(QPen(QColor("#20242c"), 0.9));
        p.setBrush(QColor("#f3f5fa"));
        for (int bar = 0; bar < 3; ++bar) {
            const double angle = 6.283185307179586 * (double(frame) / trayFrames - double(bar) / 3);
            const double h = state == TrayIcon::Idle ? still[bar] : 11 - 6 * std::cos(angle);
            p.drawRoundedRect(QRectF(5.5 + 4.5 * bar, 11 - h / 2, 3, h), 1.5, 1.5);
        }
        p.end();
        icon.addPixmap(image);
    }
    return icon;
}

namespace {
void meter(QPainter &p, int x, int center, double level, QColor color)
{
    p.setPen(Qt::NoPen);
    for (int i = 0; i < 7; ++i) {
        color.setAlpha(level * 7 > i ? 240 : 50);
        p.setBrush(color);
        const int h = 5 + i;
        p.drawRoundedRect(QRectF(x + i * 5, center - h / 2.0, 3, h), 1.5, 1.5);
    }
}

void transcript(QPainter &p, const QRect &box, const QJsonObject &data, const QString &empty)
{
    const auto stable = data.value("stable").toString();
    const auto draft = data.value("tentative").toString();
    QTextDocument doc;
    doc.setDocumentMargin(0);
    QFont font = QGuiApplication::font();
    font.setPixelSize(17);
    doc.setDefaultFont(font);
    doc.setTextWidth(box.width());
    QTextCursor cursor(&doc);
    QTextCharFormat fmt;
    fmt.setForeground(QColor("#f8f9fc"));
    cursor.insertText(stable, fmt);
    if (!stable.isEmpty() && !draft.isEmpty() && !stable.back().isSpace() && !draft.front().isSpace())
        cursor.insertText(" ", fmt);
    fmt.setForeground(QColor("#c1c9d7"));
    cursor.insertText(stable.isEmpty() && draft.isEmpty() ? empty : draft, fmt);

    // Roll by visual lines, not pixels, so new text does not leave a clipped line above it.
    doc.size();
    qreal top = 0;
    int lines = 0;
    for (auto block = doc.lastBlock(); block.isValid() && lines < 2; block = block.previous()) {
        const auto *layout = block.layout();
        for (int i = layout->lineCount() - 1; i >= 0 && lines < 2; --i, ++lines)
            top = layout->position().y() + layout->lineAt(i).y();
    }
    p.save();
    p.setClipRect(box);
    p.translate(box.left(), box.top() - top);
    doc.drawContents(&p);
    p.restore();
}

void card(QPainter &p, const QRect &box, const QString &title, const QJsonObject &data,
          const QColor &accent, bool recording, bool busy, bool compact, const QString &empty, qint64 elapsed)
{
    p.setPen(QColor(255, 255, 255, 34));
    p.setBrush(QColor(23, 26, 33, 245));
    p.drawRoundedRect(QRectF(box).adjusted(0.5, 0.5, -0.5, -0.5), 14, 14);
    const int pad = 15;
    const auto header = box.adjusted(pad, 9, -pad, 0);
    const bool showActivity = (recording || busy) && box.width() > 130;
    const int titleWidth = std::max(1, header.width() - (showActivity ? 47 : 0));
    QFont label = QGuiApplication::font();
    label.setPixelSize(13);
    label.setWeight(QFont::DemiBold);
    p.setFont(label);
    p.setPen(accent);
    p.drawText(QRect(header.x(), header.y(), titleWidth, 25), Qt::AlignVCenter,
               QFontMetrics(label).elidedText(title, Qt::ElideRight, titleWidth));
    if (showActivity && recording) {
        meter(p, header.right() - 32, header.y() + 12, data.value("level").toDouble(), accent);
    } else if (showActivity) {
        p.setPen(Qt::NoPen);
        for (int i = 0; i < 3; ++i) {
            auto color = accent;
            color.setAlpha(i == (elapsed / 150) % 3 ? 240 : 65);
            p.setBrush(color);
            p.drawEllipse(QRectF(header.right() - 20 + 8 * i, header.y() + 10, 4, 4));
        }
    }
    const auto textBox = box.adjusted(pad, 37, -pad, -10);
    if (!compact && textBox.isValid())
        transcript(p, textBox, data, empty);
}
}

static bool stackedCaption(const QString &phase, bool split)
{
    return split && (phase == "starting" || phase == "recording" || phase == "meeting" || phase == "finalizing");
}

QSize captionSize(const QString &phase, bool split)
{
    constexpr int cardHeight = 104;
    return stackedCaption(phase, split) ? QSize(560, 2 * cardHeight + captionCardGap) : QSize(720, cardHeight);
}

void paintCaption(QPainter &p, const QRect &box, const QString &phase, const QJsonObject &preview,
                  bool split, qint64 elapsed)
{
    p.setRenderHint(QPainter::Antialiasing);
    const bool recording = phase == "recording";
    const bool meeting = phase == "meeting";
    const bool starting = phase == "starting";
    const bool refining = phase == "finalizing";
    const bool busy = starting || refining;
    const bool compact = !recording && !meeting && !busy;
    const QColor blue("#a7d4ff"), violet("#d9c4ff");
    if (stackedCaption(phase, split)) {
        const int half = (box.height() - captionCardGap) / 2;
        const QString empty = recording || meeting ? QString("Listening…") : starting ? QString("Opening audio…") : QString("Refining…");
        const QRect them(box.x(), box.y(), box.width(), half);
        const QRect us(box.x(), box.y() + half + captionCardGap, box.width(), half);
        card(p, them, meeting ? "Them · Live draft" : "Them", preview.value("them").toObject(), violet, recording, busy, false, empty, elapsed);
        card(p, us, meeting ? "Us · Live draft" : "Us", preview.value("us").toObject(), blue, recording, busy, false, empty, elapsed);
        return;
    }
    QString title = "Quick Scribe";
    if (meeting) title = "Meeting · Live draft";
    else if (starting) title = "Opening audio…";
    else if (refining) title = "Refining transcript…";
    else if (phase == "copied") title = "Copied to clipboard";
    else if (phase == "ready") title = "Result ready · audiomemo dictate last";
    else if (phase == "failed") title = "No result · Check audiomemo status";
    const auto rect = compact || starting ? box.adjusted(0, box.height() - 44, 0, 0) : box;
    card(p, rect, title, preview, blue, recording, busy, compact || starting,
         recording || meeting ? QString("Listening…") : QString(), elapsed);
}
}
