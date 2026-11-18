#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <AVFoundation/AVFoundation.h>
#import <Carbon/Carbon.h>
#import <Security/AuthSession.h>
#import <unistd.h>
#include "desktop_darwin.h"
#include "_cgo_export.h"

static NSString *text(const char *s) {
    return [NSString stringWithUTF8String:s] ?: @"";
}

static NSDictionary *session(void) {
    return CFBridgingRelease(CGSessionCopyCurrentDictionary());
}

static BOOL isConsoleSession(NSDictionary *info) {
    return [info[(__bridge NSString *)kCGSessionUserIDKey] unsignedIntValue] == getuid() &&
        [info[(__bridge NSString *)kCGSessionOnConsoleKey] boolValue] &&
        [info[(__bridge NSString *)kCGSessionLoginDoneKey] boolValue];
}

@interface AMCaptionPanel : NSPanel
@end
@implementation AMCaptionPanel
- (BOOL)canBecomeKeyWindow { return NO; }
- (BOOL)canBecomeMainWindow { return NO; }
@end

@interface AMCaptionView : NSView
@property(nonatomic, copy) NSAttributedString *caption;
@end
@implementation AMCaptionView
- (BOOL)isFlipped { return YES; }
- (void)drawRect:(NSRect)dirty {
    (void)dirty;
    [[NSColor colorWithWhite:0.06 alpha:0.92] setFill];
    [[NSBezierPath bezierPathWithRoundedRect:self.bounds xRadius:12 yRadius:12] fill];
    if (!self.caption.length) return;

    // Paint the last two visual lines, rather than letting a long stable
    // prefix permanently push the tentative words off screen.
    NSTextStorage *storage = [[NSTextStorage alloc] initWithAttributedString:self.caption];
    NSLayoutManager *layout = [NSLayoutManager new];
    NSTextContainer *container = [[NSTextContainer alloc]
        initWithSize:NSMakeSize(MAX(1, NSWidth(self.bounds) - 32), CGFLOAT_MAX)];
    container.lineFragmentPadding = 0;
    [layout addTextContainer:container];
    [storage addLayoutManager:layout];
    NSRange glyphs = [layout glyphRangeForTextContainer:container];
    __block NSRange previous = NSMakeRange(NSNotFound, 0), last = previous;
    __block NSRect previousRect = NSZeroRect, lastRect = NSZeroRect;
    [layout enumerateLineFragmentsForGlyphRange:glyphs
        usingBlock:^(NSRect rect, NSRect used, NSTextContainer *tc, NSRange range, BOOL *stop) {
            (void)used;
            (void)tc;
            (void)stop;
            previous = last;
            previousRect = lastRect;
            last = range;
            lastRect = rect;
        }];
    if (last.location == NSNotFound) return;
    NSRange visible = previous.location == NSNotFound ? last : NSUnionRange(previous, last);
    CGFloat top = NSMinY(previous.location == NSNotFound ? lastRect : previousRect);
    CGFloat height = NSMaxY(lastRect) - top;
    [layout drawGlyphsForGlyphRange:visible atPoint:NSMakePoint(16, (NSHeight(self.bounds) - height) / 2 - top)];
}
@end

@interface AMTarget : NSObject
@property(nonatomic) uint64_t token;
@property(nonatomic) pid_t pid;
@property(nonatomic, strong) NSDate *launch;
@property(nonatomic, strong) id window;
@property(nonatomic, strong) id field;
@property(nonatomic, strong) NSNumber *display;
@property(nonatomic) BOOL changed;
@end
@implementation AMTarget
@end

static id attribute(AXUIElementRef element, CFStringRef name) {
    CFTypeRef value = NULL;
    if (AXUIElementCopyAttributeValue(element, name, &value) != kAXErrorSuccess) return nil;
    return CFBridgingRelease(value);
}

static AXUIElementRef asElement(id object) {
    CFTypeRef ref = (__bridge CFTypeRef)object;
    return ref && CFGetTypeID(ref) == AXUIElementGetTypeID() ? (AXUIElementRef)ref : NULL;
}

static NSNumber *screenID(NSScreen *screen) {
    return screen.deviceDescription[@"NSScreenNumber"];
}

static NSNumber *pointerDisplay(void) {
    NSPoint pointer = NSEvent.mouseLocation;
    for (NSScreen *screen in NSScreen.screens) {
        if (NSPointInRect(pointer, screen.frame)) return screenID(screen);
    }
    return screenID(NSScreen.mainScreen);
}

static NSNumber *windowDisplay(AXUIElementRef window) {
    id pos = attribute(window, kAXPositionAttribute), size = attribute(window, kAXSizeAttribute);
    CFTypeRef p = (__bridge CFTypeRef)pos, s = (__bridge CFTypeRef)size;
    CGPoint origin;
    CGSize extent;
    if (!p || !s || CFGetTypeID(p) != AXValueGetTypeID() || CFGetTypeID(s) != AXValueGetTypeID() ||
        !AXValueGetValue((AXValueRef)p, kAXValueCGPointType, &origin) ||
        !AXValueGetValue((AXValueRef)s, kAXValueCGSizeType, &extent)) return nil;
    CGDirectDisplayID displays[1];
    uint32_t count;
    CGPoint center = CGPointMake(origin.x + extent.width / 2, origin.y + extent.height / 2);
    if (CGGetDisplaysWithPoint(center, 1, displays, &count) != kCGErrorSuccess || count != 1) return nil;
    return @(displays[0]);
}

static BOOL fieldIsEditable(AXUIElementRef field) {
    id role = attribute(field, kAXRoleAttribute);
    if (![(__bridge NSString *)kAXTextFieldRole isEqual:role] &&
        ![(__bridge NSString *)kAXTextAreaRole isEqual:role] &&
        ![(__bridge NSString *)kAXComboBoxRole isEqual:role]) return NO;
    if ([(__bridge NSString *)kAXSecureTextFieldSubrole isEqual:attribute(field, kAXSubroleAttribute)] ||
        [@NO isEqual:attribute(field, kAXEnabledAttribute)]) return NO;
    Boolean settable = false;
    return AXUIElementIsAttributeSettable(field, kAXValueAttribute, &settable) == kAXErrorSuccess && settable;
}

static AMTarget *snapshot(BOOL locateDisplay) {
    AMTarget *target = [AMTarget new];
    if (locateDisplay) target.display = pointerDisplay();
    if (!AXIsProcessTrusted()) return target;
    NSRunningApplication *front = NSWorkspace.sharedWorkspace.frontmostApplication;
    pid_t pid = front.processIdentifier;
    if (pid <= 0 || pid == getpid() || !front.launchDate) return target;

    AXUIElementRef app = AXUIElementCreateApplication(pid);
    if (!app) return target;
    // An unresponsive remote AX server must not stall the UI while it fences a paste.
    if (AXUIElementSetMessagingTimeout(app, 0.075f) == kAXErrorSuccess) {
        id window = attribute(app, kAXFocusedWindowAttribute);
        AXUIElementRef windowRef = asElement(window);
        if (windowRef && AXUIElementSetMessagingTimeout(windowRef, 0.075f) == kAXErrorSuccess) {
            if (locateDisplay) target.display = windowDisplay(windowRef) ?: target.display;
            id field = attribute(app, kAXFocusedUIElementAttribute);
            AXUIElementRef fieldRef = asElement(field);
            if (fieldRef && AXUIElementSetMessagingTimeout(fieldRef, 0.075f) == kAXErrorSuccess) {
                id fieldWindow = attribute(fieldRef, kAXWindowAttribute);
                if (asElement(fieldWindow) && CFEqual(asElement(fieldWindow), windowRef) && fieldIsEditable(fieldRef)) {
                    target.pid = pid;
                    target.launch = front.launchDate;
                    target.window = window;
                    target.field = field;
                }
            }
        }
    }
    CFRelease(app);
    return target;
}

static BOOL sameFocus(AMTarget *a, AMTarget *b) {
    return a.pid > 0 && a.pid == b.pid && [a.launch isEqualToDate:b.launch] &&
        a.window && b.window && CFEqual(asElement(a.window), asElement(b.window)) &&
        a.field && b.field && CFEqual(asElement(a.field), asElement(b.field));
}

static uint64_t nextTarget;

@interface AMDesktop : NSObject
@property(nonatomic) uintptr_t handle;
@property(nonatomic, strong) NSStatusItem *item;
@property(nonatomic, strong) NSMenuItem *status;
@property(nonatomic, strong) NSMenuItem *warning;
@property(nonatomic, strong) NSMenuItem *dictate;
@property(nonatomic, strong) NSMenuItem *cancel;
@property(nonatomic, strong) NSMenuItem *meeting;
@property(nonatomic, strong) NSMenuItem *chooseMeeting;
@property(nonatomic, strong) AMCaptionPanel *panel;
@property(nonatomic, strong) AMCaptionView *caption;
@property(nonatomic, copy) NSString *captionSession;
@property(nonatomic, strong) NSNumber *captionDisplay;
@property(nonatomic, strong) NSNumber *triggerDisplay;
@property(nonatomic, strong) AMTarget *gestureTarget;
@property(nonatomic, strong) AMTarget *target;
@property(nonatomic) BOOL locked;
@property(nonatomic, strong) NSArray<id> *observers;
@property(nonatomic) BOOL keyDown;
@property(nonatomic, strong) NSTimer *timer;
@property(nonatomic) EventHandlerRef handler;
@property(nonatomic) EventHotKeyRef hotKey;
- (void)perform:(AMAction)action device:(NSString *)device;
- (BOOL)active;
@end

static const EventHotKeyID hotKeyID = {0x41754d65, 1}; // AuMe

static OSStatus onHotKey(EventHandlerCallRef call, EventRef event, void *data) {
    (void)call;
    EventHotKeyID id = {0};
    if (GetEventParameter(event, kEventParamDirectObject, typeEventHotKeyID, NULL, sizeof(id), NULL, &id) != noErr ||
        id.signature != hotKeyID.signature || id.id != hotKeyID.id) return eventNotHandledErr;
    AMDesktop *ui = (__bridge AMDesktop *)data;
    if (GetEventKind(event) == kEventHotKeyReleased) {
        ui.keyDown = NO;
    } else if (!ui.keyDown) {
        ui.keyDown = YES;
        [ui perform:AMToggle device:nil];
    }
    return noErr;
}

@implementation AMDesktop
- (BOOL)active {
    NSDictionary *info = session();
    // The lock notification handles transitions; this key, when present,
    // additionally covers launch while the screen is already locked.
    return !self.locked && isConsoleSession(info) && ![info[@"CGSSessionScreenIsLocked"] boolValue];
}
- (NSMenuItem *)add:(NSString *)title tag:(NSInteger)tag menu:(NSMenu *)menu {
    NSMenuItem *row = [[NSMenuItem alloc] initWithTitle:title action:@selector(select:) keyEquivalent:@""];
    row.target = self;
    row.tag = tag;
    [menu addItem:row];
    return row;
}
- (void)setUpMenu {
    self.item = [NSStatusBar.systemStatusBar statusItemWithLength:NSVariableStatusItemLength];
    self.item.button.title = @"AudioMemo";
    NSMenu *menu = [NSMenu new];
    menu.autoenablesItems = NO;
    self.status = [self add:@"Ready" tag:-1 menu:menu];
    self.status.enabled = NO;
    self.warning = [self add:@"" tag:-1 menu:menu];
    self.warning.enabled = NO;
    self.warning.hidden = YES;
    [menu addItem:NSMenuItem.separatorItem];
    self.dictate = [self add:@"Start Quick Scribe" tag:AMStartDictate menu:menu];
    self.cancel = [self add:@"Cancel Quick Scribe" tag:AMCancelDictate menu:menu];
    self.cancel.hidden = YES;
    self.meeting = [self add:@"Start Meeting" tag:AMStartMeeting menu:menu];
    self.chooseMeeting = [self add:@"Choose Meeting Group…" tag:-2 menu:menu];
    [self add:@"Copy Last Result" tag:AMCopyLast menu:menu];
    [menu addItem:NSMenuItem.separatorItem];
    NSMenuItem *key = [self add:@"Global shortcut: Control–Option–Space" tag:-1 menu:menu];
    key.enabled = NO;
    [self add:@"Quit AudioMemo" tag:AMQuit menu:menu];
    self.item.menu = menu;

    EventTypeSpec events[] = {{kEventClassKeyboard, kEventHotKeyPressed}, {kEventClassKeyboard, kEventHotKeyReleased}};
    EventHandlerRef handler = NULL;
    EventHotKeyRef hotKey = NULL;
    if (InstallEventHandler(GetApplicationEventTarget(), onHotKey, 2, events, (__bridge void *)self, &handler) != noErr ||
        RegisterEventHotKey(kVK_Space, controlKey | optionKey, hotKeyID, GetApplicationEventTarget(), 0, &hotKey) != noErr) {
        key.title = @"Global shortcut unavailable (Control–Option–Space). Use the menu.";
        self.item.button.toolTip = key.title;
    }
    self.handler = handler;
    self.hotKey = hotKey;
}
- (void)setUpCaption {
    self.panel = [[AMCaptionPanel alloc] initWithContentRect:NSMakeRect(0, 0, 500, 68)
        styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel
        backing:NSBackingStoreBuffered defer:YES];
    self.panel.opaque = NO;
    self.panel.backgroundColor = NSColor.clearColor;
    self.panel.hasShadow = NO;
    self.panel.ignoresMouseEvents = YES;
    self.panel.hidesOnDeactivate = NO;
    self.panel.level = NSFloatingWindowLevel;
    self.panel.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces |
        NSWindowCollectionBehaviorFullScreenAuxiliary | NSWindowCollectionBehaviorIgnoresCycle;
    self.caption = [AMCaptionView new];
    self.panel.contentView = self.caption;
}
- (void)select:(NSMenuItem *)row {
    if (row.tag == -2) {
        if (![self active] || !self.item.button.window.isVisible) return;
        NSAlert *alert = [NSAlert new];
        alert.messageText = @"Choose a meeting group";
        alert.informativeText = @"Enter a device group name from your AudioMemo configuration.";
        NSTextField *field = [[NSTextField alloc] initWithFrame:NSMakeRect(0, 0, 240, 24)];
        alert.accessoryView = field;
        alert.window.initialFirstResponder = field;
        [alert addButtonWithTitle:@"Start Meeting"];
        [alert addButtonWithTitle:@"Cancel"];
        if ([alert runModal] == NSAlertFirstButtonReturn) {
            NSString *name = [field.stringValue stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceAndNewlineCharacterSet];
            if (name.length) [self perform:AMStartMeeting device:name];
        }
    } else if (row.tag >= 0) {
        [self perform:(AMAction)row.tag device:nil];
    }
}
- (void)perform:(AMAction)action device:(NSString *)device {
    if (action != AMQuit && ![self active]) return;
    if ((action == AMStartMeeting || action == AMStartDictate || action == AMToggle) && !self.item.button.window.isVisible) return;
    AMTarget *prior = self.gestureTarget;
    if (action == AMStartDictate || (action == AMToggle && self.dictate.tag != AMStopDictate)) {
        self.gestureTarget = snapshot(YES);
        if (action == AMStartDictate) self.gestureTarget.display = pointerDisplay();
    }
    if (!amGoAction(self.handle, action, (char *)device.UTF8String)) self.gestureTarget = prior;
}
- (void)invalidateTarget:(NSNotification *)notice {
    NSRunningApplication *app = notice.userInfo[NSWorkspaceApplicationKey];
    if (self.target.pid && app.processIdentifier != self.target.pid) self.target.changed = YES;
    if (self.gestureTarget.pid && app.processIdentifier != self.gestureTarget.pid) self.gestureTarget.changed = YES;
}
- (void)lock:(NSNotification *)notice {
    (void)notice;
    self.locked = YES;
    self.target.changed = YES;
    self.gestureTarget.changed = YES;
    self.keyDown = NO;
    [self.panel orderOut:nil];
}
- (void)unlock:(NSNotification *)notice {
    (void)notice;
    self.locked = NO;
}
- (void)tick:(NSTimer *)timer {
    (void)timer;
    amGoTick(self.handle, (__bridge void *)self);
}
- (void)start {
    [self setUpMenu];
    [self setUpCaption];
    NSNotificationCenter *workspace = NSWorkspace.sharedWorkspace.notificationCenter;
    NSOperationQueue *main = NSOperationQueue.mainQueue;
    __weak AMDesktop *weak = self;
    self.observers = @[
        [workspace addObserverForName:NSWorkspaceDidActivateApplicationNotification object:nil queue:main
            usingBlock:^(NSNotification *notice) { [weak invalidateTarget:notice]; }],
        [workspace addObserverForName:NSWorkspaceSessionDidResignActiveNotification object:nil queue:main
            usingBlock:^(NSNotification *notice) { [weak lock:notice]; }],
        [workspace addObserverForName:NSWorkspaceSessionDidBecomeActiveNotification object:nil queue:main
            usingBlock:^(NSNotification *notice) { [weak unlock:notice]; }],
    ];
    NSDistributedNotificationCenter *distributed = NSDistributedNotificationCenter.defaultCenter;
    [distributed addObserver:self selector:@selector(lock:) name:@"com.apple.screenIsLocked" object:nil];
    [distributed addObserver:self selector:@selector(unlock:) name:@"com.apple.screenIsUnlocked" object:nil];
    self.timer = [NSTimer timerWithTimeInterval:1.0/30.0 target:self selector:@selector(tick:) userInfo:nil repeats:YES];
    [NSRunLoop.mainRunLoop addTimer:self.timer forMode:NSRunLoopCommonModes];
}
- (void)tearDown {
    [self.timer invalidate];
    self.timer = nil;
    for (id observer in self.observers) [NSWorkspace.sharedWorkspace.notificationCenter removeObserver:observer];
    [NSDistributedNotificationCenter.defaultCenter removeObserver:self];
    if (self.hotKey) UnregisterEventHotKey(self.hotKey);
    if (self.handler) RemoveEventHandler(self.handler);
    [self.panel orderOut:nil];
    if (self.item) [NSStatusBar.systemStatusBar removeStatusItem:self.item];
}
@end

int am_ui_run(uintptr_t handle) {
    @autoreleasepool {
        if (!NSThread.isMainThread) return AMNotMainThread;
        SessionAttributeBits attributes = 0;
        if (SessionGetInfo(callerSecuritySession, NULL, &attributes) != errSessionSuccess ||
            !(attributes & sessionHasGraphicAccess) || !isConsoleSession(session())) return AMNoAquaSession;
        [NSApplication sharedApplication];
        if (![NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory]) return AMNoAquaSession;
        AMDesktop *ui = [AMDesktop new];
        ui.handle = handle;
        [ui start];
        if (!ui.item.button) {
            [ui tearDown];
            return AMStatusItemUnavailable;
        }
        [NSApp run];
        [ui tearDown];
        return AMUIStopped;
    }
}

void am_ui_stop(void) {
    if (!NSApp.isRunning) return;
    if (NSApp.modalWindow) [NSApp stopModalWithCode:NSModalResponseAbort];
    [NSApp stop:nil];
    // A timer can stop the run loop while AppKit is waiting for an event.
    [NSApp postEvent:[NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint
        modifierFlags:0 timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0] atStart:YES];
}

void am_ui_render(void *ptr, const char *title, const char *status, const char *warning,
                  int dictate, int meeting, int cancel) {
    AMDesktop *ui = (__bridge AMDesktop *)ptr;
    NSString *label = text(title);
    NSMutableAttributedString *display = [[NSMutableAttributedString alloc] initWithString:label];
    if ([label hasPrefix:@"●"]) {
        [display addAttribute:NSForegroundColorAttributeName value:NSColor.systemRedColor range:NSMakeRange(0, 1)];
    }
    ui.item.button.attributedTitle = display;
    ui.status.title = text(status);
    // A terse single line also keeps a backend diagnostic from sizing the menu off-screen.
    NSString *message = [text(warning) stringByReplacingOccurrencesOfString:@"\n" withString:@" · "];
    NSRange prefix = NSMakeRange(0, MIN(message.length, 127));
    ui.warning.title = message.length <= 128 ? message :
        [[message substringWithRange:[message rangeOfComposedCharacterSequencesForRange:prefix]] stringByAppendingString:@"…"];
    ui.warning.toolTip = message;
    ui.warning.hidden = !message.length;
    ui.dictate.enabled = dictate != 0;
    ui.dictate.tag = dictate == 1 ? AMStartDictate : dictate == 2 ? AMStopDictate : -1;
    ui.dictate.title = dictate == 2 ? @"Stop Quick Scribe" : @"Start Quick Scribe";
    ui.cancel.hidden = !cancel;
    ui.meeting.enabled = meeting != 0;
    ui.meeting.tag = meeting == 1 ? AMStartMeeting : meeting == 2 ? AMStopMeeting : -1;
    ui.meeting.title = meeting == 2 ? @"Stop Meeting" : @"Start Meeting";
    ui.chooseMeeting.enabled = meeting == 1;
}

void am_ui_caption(void *ptr, const char *sessionID, const char *stable, const char *tentative, int meeting) {
    AMDesktop *ui = (__bridge AMDesktop *)ptr;
    NSString *session = text(sessionID), *done = text(stable), *draft = text(tentative);
    if (![ui active] || !session.length || (!done.length && !draft.length)) {
        [ui.panel orderOut:nil];
        ui.caption.caption = nil;
        ui.captionSession = nil;
        return;
    }
    if (![ui.captionSession isEqualToString:session]) {
        ui.captionSession = session;
        ui.captionDisplay = meeting ? pointerDisplay() : (ui.triggerDisplay ?: pointerDisplay());
    }
    NSScreen *screen = nil;
    for (NSScreen *candidate in NSScreen.screens) {
        if ([ui.captionDisplay isEqual:screenID(candidate)]) {
            screen = candidate;
            break;
        }
    }
    if (!screen) screen = NSScreen.mainScreen;
    if (!screen) return;
    NSRect work = screen.visibleFrame;
    CGFloat width = MIN(660, NSWidth(work) - 24);
    if (width <= 32) return;
    NSRect frame = NSMakeRect(NSMidX(work) - width / 2, NSMinY(work) + 18, width, 68);
    if (!NSEqualRects(ui.panel.frame, frame)) [ui.panel setFrame:frame display:NO];
    NSMutableParagraphStyle *style = [NSMutableParagraphStyle new];
    style.alignment = NSTextAlignmentCenter;
    NSDictionary *committed = @{NSFontAttributeName: [NSFont systemFontOfSize:17 weight:NSFontWeightMedium],
        NSForegroundColorAttributeName: NSColor.whiteColor, NSParagraphStyleAttributeName: style};
    NSMutableDictionary *draftStyle = [committed mutableCopy];
    draftStyle[NSForegroundColorAttributeName] = [NSColor colorWithWhite:0.78 alpha:1];
    // Even the stable portion of a meeting preview is a draft, not the bundle transcript.
    NSMutableAttributedString *caption = [[NSMutableAttributedString alloc] initWithString:done
        attributes:meeting ? draftStyle : committed];
    if (draft.length) {
        if (done.length) [caption appendAttributedString:[[NSAttributedString alloc] initWithString:@" " attributes:committed]];
        [caption appendAttributedString:[[NSAttributedString alloc] initWithString:draft attributes:draftStyle]];
    }
    ui.caption.caption = caption;
    ui.caption.needsDisplay = YES;
    if (!ui.panel.isVisible) [ui.panel orderFrontRegardless];
}

uint64_t am_ui_target(void *ptr, int useGesture) {
    AMDesktop *ui = (__bridge AMDesktop *)ptr;
    AMTarget *gesture = useGesture ? ui.gestureTarget : nil;
    ui.gestureTarget = nil;
    if (![ui active]) {
        ui.target = nil;
        return 0;
    }
    AMTarget *current = snapshot(!useGesture);
    if (useGesture && !gesture) {
        // A gesture whose snapshot could not be retained is not permission to
        // take the newly focused field as its original target.
        current = [AMTarget new];
        current.display = pointerDisplay();
    } else if (gesture) {
        if (gesture.pid && !sameFocus(gesture, current)) gesture.changed = YES;
        current = gesture;
    }
    current.token = ++nextTarget;
    ui.target = current;
    ui.triggerDisplay = current.display;
    return current.token;
}

int am_target_fence(int allowed, int supplied, int found, int changed, int editable, int matches) {
    if (!allowed) return AMAccessibilityDenied;
    if (!found) return supplied ? AMFocusChanged : AMTargetUnknown;
    if (changed) return AMFocusChanged;
    if (!editable) return AMTargetUnknown;
    return matches ? AMDelivered : AMFocusChanged;
}

static BOOL layoutSupportsPasteKey(void) {
    TISInputSourceRef input = TISCopyCurrentKeyboardLayoutInputSource();
    if (!input) return NO;
    CFDataRef data = TISGetInputSourceProperty(input, kTISPropertyUnicodeKeyLayoutData);
    const UCKeyboardLayout *layout = data ? (const UCKeyboardLayout *)CFDataGetBytePtr(data) : NULL;
    UInt32 deadKey = 0;
    UniCharCount length = 0;
    UniChar chars[4];
    BOOL supported = layout && UCKeyTranslate(layout, kVK_ANSI_V, kUCKeyActionDown,
        (cmdKey >> 8) & 0xff, LMGetKbdType(), kUCKeyTranslateNoDeadKeysMask, &deadKey,
        sizeof(chars) / sizeof(chars[0]), &length, chars) == noErr && length == 1 &&
        (chars[0] == 'v' || chars[0] == 'V');
    CFRelease(input);
    return supported;
}

int am_ui_deliver(void *ptr, const void *bytes, size_t length, uint64_t token, int paste, uintptr_t valid) {
    AMDesktop *ui = (__bridge AMDesktop *)ptr;
    if (![ui active]) return AMInactive;
    NSString *string = [[NSString alloc] initWithBytes:bytes length:length encoding:NSUTF8StringEncoding];
    if (!string) return AMClipboardFailed;
    NSPasteboard *clipboard = NSPasteboard.generalPasteboard;
    if (!clipboard) return AMClipboardFailed;
    if (!amGoCanDeliver(valid)) return AMDeliveryCanceled;
    NSInteger count = [clipboard clearContents];
    if (![clipboard setString:string forType:NSPasteboardTypeString]) return AMClipboardFailed;
    if (!paste) return AMDelivered;

    // Copy is intentional, including after successful paste. There is no async
    // clipboard restoration that can race the target's read or a newer copy.
    AMTarget *original = token && ui.target.token == token ? ui.target : nil;
    if (original) ui.target = nil;
    BOOL allowed = AXIsProcessTrusted() && CGPreflightPostEventAccess();
    BOOL matches = allowed && original.field && !original.changed && sameFocus(original, snapshot(NO));
    int fence = am_target_fence(allowed, token != 0, original != nil, original.changed, original.field != nil, matches);
    if (fence != AMDelivered) return fence;

    CGEventFlags modifiers = kCGEventFlagMaskCommand | kCGEventFlagMaskControl | kCGEventFlagMaskShift |
        kCGEventFlagMaskAlternate | kCGEventFlagMaskSecondaryFn;
    if (CGEventSourceFlagsState(kCGEventSourceStateHIDSystemState) & modifiers) return AMModifiersHeld;
    if (clipboard.changeCount != count) return AMClipboardChanged;
    if (!layoutSupportsPasteKey()) return AMPasteUnavailable;
    CGEventRef down = CGEventCreateKeyboardEvent(NULL, kVK_ANSI_V, true);
    CGEventRef up = CGEventCreateKeyboardEvent(NULL, kVK_ANSI_V, false);
    int result = AMPasteUnavailable;
    if (down && up) {
        CGEventSetFlags(down, kCGEventFlagMaskCommand);
        CGEventSetFlags(up, kCGEventFlagMaskCommand);
        // Send only to the verified PID. Never raise/re-activate an app or post
        // command-V to the global event stream. AX and keyboard posting are not atomic.
        if (![ui active] || NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier != original.pid) {
            result = AMFocusChanged;
        } else if (clipboard.changeCount != count) {
            result = AMClipboardChanged;
        } else if (!amGoCanDeliver(valid)) {
            result = AMCopiedBeforeCancel;
        } else {
            CGEventPostToPid(original.pid, down);
            CGEventPostToPid(original.pid, up);
            result = AMDelivered;
        }
    }
    if (down) CFRelease(down);
    if (up) CFRelease(up);
    return result;
}

int am_microphone_status(void) {
    @autoreleasepool {
        switch ([AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeAudio]) {
        case AVAuthorizationStatusAuthorized: return AMMicAllowed;
        case AVAuthorizationStatusNotDetermined: return AMMicUndetermined;
        case AVAuthorizationStatusRestricted: return AMMicRestricted;
        case AVAuthorizationStatusDenied: return AMMicDenied;
        }
        return AMMicDenied;
    }
}

void am_microphone_request(void) {
    @autoreleasepool {
        [AVCaptureDevice requestAccessForMediaType:AVMediaTypeAudio completionHandler:^(BOOL granted) {
            (void)granted;
            amGoMicrophoneResponse();
        }];
    }
}
