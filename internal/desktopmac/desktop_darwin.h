#pragma once
#include <stdint.h>
#include <stddef.h>

// These enums also back the Go bridge's status mappings.
typedef enum {
    AMQuit, AMToggle, AMStartDictate, AMStopDictate, AMCancelDictate,
    AMStartMeeting, AMStopMeeting, AMCopyLast
} AMAction;

typedef enum {
    AMDelivered, AMInactive, AMClipboardFailed, AMAccessibilityDenied,
    AMTargetUnknown, AMFocusChanged, AMModifiersHeld, AMClipboardChanged,
    AMPasteUnavailable, AMDeliveryCanceled, AMCopiedBeforeCancel
} AMDelivery;

typedef enum {
    AMUIStopped, AMNotMainThread, AMNoAquaSession, AMStatusItemUnavailable
} AMRunResult;

typedef enum { AMMicAllowed, AMMicUndetermined, AMMicDenied, AMMicRestricted } AMMicStatus;

typedef enum { AMTrayIdle, AMTrayRecording, AMTrayRefining } AMTrayIcon;

int am_ui_run(uintptr_t handle);
void am_ui_stop(void);
void am_ui_render(void *ui, const char *title, const char *status, const char *warning,
                  int dictate, int meeting, int cancel, int icon);
void am_ui_caption(void *ui, const char *session, const char *phase, const char *stable, const char *tentative,
    double level, int split, const char *us, const char *usDraft, double usLevel,
    const char *them, const char *themDraft, double themLevel);
uint64_t am_ui_target(void *ui, int useGesture);
int am_target_fence(int allowed, int supplied, int found, int changed, int editable, int matches);
int am_ui_deliver(void *ui, const void *text, size_t length, uint64_t target, int paste, uintptr_t valid);
int am_microphone_status(void);
void am_microphone_request(void);
