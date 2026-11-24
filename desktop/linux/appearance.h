#pragma once

#include <QIcon>
#include <QJsonObject>
#include <QPainter>

namespace appearance {
enum class TrayIcon { Idle, Recording, Refining };
inline constexpr int trayFrames = 24;
inline constexpr int captionCardGap = 10;

QSize captionSize(const QString &phase, bool split);

QIcon indicator(TrayIcon state, int frame = 0);
void paintCaption(QPainter &p, const QRect &box, const QString &phase, const QJsonObject &preview,
                  bool split, qint64 elapsed);
}
