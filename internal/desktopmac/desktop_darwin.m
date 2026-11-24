#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <AVFoundation/AVFoundation.h>
#import <Carbon/Carbon.h>
#import <Security/AuthSession.h>
#import <unistd.h>
#include <math.h>
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

static const CGFloat captionCardHeight = 104;
static const CGFloat captionCardGap = 10;

@interface AMCaptionView : NSView
@property(nonatomic, copy) NSAttributedString *caption;
@property(nonatomic, copy) NSAttributedString *remoteCaption;
@property(nonatomic, copy) NSString *phase;
@property(nonatomic) BOOL split;
@property(nonatomic) double level;
@property(nonatomic) double remoteLevel;
@end
@implementation AMCaptionView
- (BOOL)isFlipped { return YES; }
- (void)drawCaption:(NSAttributedString *)caption inRect:(NSRect)box {
    if (!caption.length) return;

    // Paint the last two visual lines, rather than letting a long stable
    // prefix permanently push the tentative words off screen.
    NSTextStorage *storage = [[NSTextStorage alloc] initWithAttributedString:caption];
    NSLayoutManager *layout = [NSLayoutManager new];
    NSTextContainer *container = [[NSTextContainer alloc]
        initWithSize:NSMakeSize(MAX(1, NSWidth(box)), CGFLOAT_MAX)];
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
    [NSGraphicsContext saveGraphicsState];
    NSRectClip(box);
    [layout drawGlyphsForGlyphRange:visible atPoint:NSMakePoint(NSMinX(box), NSMinY(box) - top)];
    [NSGraphicsContext restoreGraphicsState];
}
- (void)meterAt:(NSPoint)origin level:(double)level color:(NSColor *)color {
    for (int i = 0; i < 7; i++) {
        [[color colorWithAlphaComponent:level * 7 > i ? 0.94 : 0.20] setFill];
        CGFloat h = 5 + i;
        NSRect rect = NSMakeRect(origin.x + i * 5, origin.y - h / 2, 3, h);
        [[NSBezierPath bezierPathWithRoundedRect:rect xRadius:1.5 yRadius:1.5] fill];
    }
}
- (void)card:(NSRect)box title:(NSString *)title accent:(NSColor *)accent
     caption:(NSAttributedString *)caption level:(double)level recording:(BOOL)recording busy:(BOOL)busy {
    NSBezierPath *path = [NSBezierPath bezierPathWithRoundedRect:NSInsetRect(box, 0.5, 0.5) xRadius:14 yRadius:14];
    [[NSColor colorWithSRGBRed:23.0/255 green:26.0/255 blue:33.0/255 alpha:0.96] setFill];
    [path fill];
    [[NSColor colorWithWhite:1 alpha:0.13] setStroke];
    [path stroke];

    BOOL showActivity = (recording || busy) && NSWidth(box) > 130;
    NSMutableParagraphStyle *style = [NSMutableParagraphStyle new];
    style.lineBreakMode = NSLineBreakByTruncatingTail;
    NSDictionary *header = @{NSFontAttributeName: [NSFont systemFontOfSize:13 weight:NSFontWeightSemibold],
        NSForegroundColorAttributeName: accent, NSParagraphStyleAttributeName: style};
    [title drawInRect:NSMakeRect(NSMinX(box) + 15, NSMinY(box) + 13,
        MAX(1, NSWidth(box) - 30 - (showActivity ? 47 : 0)), 20) withAttributes:header];
    if (showActivity && recording) {
        [self meterAt:NSMakePoint(NSMaxX(box) - 48, NSMinY(box) + 21) level:level color:accent];
    } else if (showActivity) {
        int step = (int)(NSProcessInfo.processInfo.systemUptime / 0.15) % 3;
        for (int i = 0; i < 3; i++) {
            [[accent colorWithAlphaComponent:i == step ? 0.94 : 0.25] setFill];
            [[NSBezierPath bezierPathWithOvalInRect:NSMakeRect(NSMaxX(box) - 36 + i * 8, NSMinY(box) + 19, 4, 4)] fill];
        }
    }
    [self drawCaption:caption inRect:NSMakeRect(NSMinX(box) + 15, NSMinY(box) + 37, NSWidth(box) - 30, 48)];
}
- (void)drawRect:(NSRect)dirty {
    (void)dirty;
    NSString *phase = self.phase;
    BOOL recording = [phase isEqualToString:@"recording"];
    BOOL refining = [phase isEqualToString:@"finalizing"];
    BOOL starting = [phase isEqualToString:@"starting"];
    BOOL meeting = [phase isEqualToString:@"meeting"];
    BOOL busy = starting || refining;
    BOOL compact = !recording && !busy && !meeting;
    NSColor *blue = [NSColor colorWithSRGBRed:0.65 green:0.83 blue:1 alpha:1];
    NSColor *violet = [NSColor colorWithSRGBRed:0.85 green:0.77 blue:1 alpha:1];
    NSRect box = self.bounds;
    if (self.split) {
        CGFloat half = (NSHeight(box) - captionCardGap) / 2;
        NSRect them = NSMakeRect(NSMinX(box), NSMinY(box), NSWidth(box), half);
        NSRect us = NSOffsetRect(them, 0, half + captionCardGap);
        [self card:them title:@"Them" accent:violet caption:self.remoteCaption level:self.remoteLevel recording:recording busy:busy];
        [self card:us title:@"Us" accent:blue caption:self.caption level:self.level recording:recording busy:busy];
        return;
    }
    NSString *title = @"Quick Scribe";
    if (starting) title = @"Opening audio…";
    else if (refining) title = @"Refining transcript…";
    else if (meeting) title = @"Meeting · Live draft";
    else if ([phase isEqualToString:@"copied"]) title = @"Copied to clipboard";
    else if ([phase isEqualToString:@"ready"]) title = @"Result ready · audiomemo dictate last";
    else if ([phase isEqualToString:@"failed"]) title = @"No result · Check audiomemo status";
    if (compact || starting) box = NSMakeRect(NSMinX(box), NSMaxY(box) - 44, NSWidth(box), 44);
    [self card:box title:title accent:blue caption:compact || starting ? nil : self.caption
        level:self.level recording:recording busy:busy];
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

static const int trayFrames = 24;

static NSImage *trayImage(AMTrayIcon icon, int frame) {
    NSImage *image = [NSImage imageWithSize:NSMakeSize(22, 22) flipped:YES drawingHandler:^BOOL(NSRect rect) {
        (void)rect;
        static const int still[] = {8, 17, 12};
        [NSColor.blackColor setFill];
        for (int bar = 0; bar < 3; ++bar) {
            double angle = 2 * M_PI * ((double)frame / trayFrames - (double)bar / 3);
            CGFloat h = icon == AMTrayIdle ? still[bar] : 11 - 6 * cos(angle);
            NSRect bounds = NSMakeRect(5.5 + 4.5 * bar, 11 - h / 2, 3, h);
            [[NSBezierPath bezierPathWithRoundedRect:bounds xRadius:1.5 yRadius:1.5] fill];
        }
        return YES;
    }];
    image.template = YES;
    return image;
}

@interface AMDesktop : NSObject
@property(nonatomic) uintptr_t handle;
@property(nonatomic, strong) NSStatusItem *item;
@property(nonatomic) AMTrayIcon trayIcon;
@property(nonatomic) NSTimeInterval trayStarted;
@property(nonatomic) int trayFrame;
@property(nonatomic, copy) NSArray<NSImage *> *trayAnimation;
@property(nonatomic, strong) NSMenuItem *status;
@property(nonatomic, strong) NSMenuItem *warning;
@property(nonatomic, strong) NSMenuItem *dictate;
@property(nonatomic, strong) NSMenuItem *dictateBoth;
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
@property(nonatomic) UInt32 keysDown;
@property(nonatomic, strong) NSTimer *timer;
@property(nonatomic) EventHandlerRef handler;
@property(nonatomic) EventHotKeyRef hotKey;
@property(nonatomic) EventHotKeyRef hotKeyBoth;
- (void)perform:(AMAction)action device:(NSString *)device;
- (BOOL)active;
@end

static const EventHotKeyID hotKeyID = {0x41754d65, 1}; // AuMe

static const EventHotKeyID hotKeyBothID = {0x41754d65, 2};

static OSStatus onHotKey(EventHandlerCallRef call, EventRef event, void *data) {
    (void)call;
    EventHotKeyID id = {0};
    if (GetEventParameter(event, kEventParamDirectObject, typeEventHotKeyID, NULL, sizeof(id), NULL, &id) != noErr ||
        id.signature != hotKeyID.signature || (id.id != hotKeyID.id && id.id != hotKeyBothID.id)) return eventNotHandledErr;
    AMDesktop *ui = (__bridge AMDesktop *)data;
    const UInt32 key = 1U << id.id;
    if (GetEventKind(event) == kEventHotKeyReleased) {
        ui.keysDown &= ~key;
    } else if (!(ui.keysDown & key)) {
        ui.keysDown |= key;
        [ui perform:id.id == hotKeyBothID.id ? AMToggleBoth : AMToggle device:nil];
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
- (void)updateTrayIcon:(AMTrayIcon)icon {
    if (icon != self.trayIcon || !self.item.button.image) {
        self.trayIcon = icon;
        self.trayStarted = NSProcessInfo.processInfo.systemUptime;
        self.trayFrame = -1;
    } else if (icon == AMTrayIdle) {
        return;
    }
    if (icon == AMTrayIdle) {
        self.item.button.image = trayImage(icon, 0);
        return;
    }
    if (!self.trayAnimation) {
        NSMutableArray<NSImage *> *frames = [NSMutableArray arrayWithCapacity:trayFrames];
        for (int frame = 0; frame < trayFrames; frame++) [frames addObject:trayImage(icon, frame)];
        self.trayAnimation = frames;
    }
    double frameSeconds = icon == AMTrayRecording ? 0.038 : 0.065;
    int frame = (int)((NSProcessInfo.processInfo.systemUptime - self.trayStarted) / frameSeconds) % trayFrames;
    if (frame != self.trayFrame) {
        self.trayFrame = frame;
        self.item.button.image = self.trayAnimation[frame];
    }
}
- (void)setUpMenu {
    self.item = [NSStatusBar.systemStatusBar statusItemWithLength:NSVariableStatusItemLength];
    self.item.button.imagePosition = NSImageOnly;
    [self.item.button setAccessibilityLabel:@"AudioMemo"];
    [self updateTrayIcon:AMTrayIdle];
    NSMenu *menu = [NSMenu new];
    menu.autoenablesItems = NO;
    self.status = [self add:@"Ready" tag:-1 menu:menu];
    self.status.enabled = NO;
    self.warning = [self add:@"" tag:-1 menu:menu];
    self.warning.enabled = NO;
    self.warning.hidden = YES;
    [menu addItem:NSMenuItem.separatorItem];
    self.dictate = [self add:@"Start Quick Scribe" tag:AMStartDictate menu:menu];
    self.dictateBoth = [self add:@"Start Quick Scribe · Us and Them" tag:AMStartDictateBoth menu:menu];
    self.cancel = [self add:@"Cancel Quick Scribe" tag:AMCancelDictate menu:menu];
    self.cancel.hidden = YES;
    self.meeting = [self add:@"Start Meeting" tag:AMStartMeeting menu:menu];
    self.chooseMeeting = [self add:@"Choose Meeting Group…" tag:-2 menu:menu];
    [self add:@"Copy Last Result" tag:AMCopyLast menu:menu];
    // A host can map the Dictation consumer key to F18 without an event tap.
    BOOL useF18 = [NSProcessInfo.processInfo.environment[@"AUDIOMEMO_MAC_HOTKEY"] isEqualToString:@"F18"];
    [menu addItem:NSMenuItem.separatorItem];
    [self add:@"Quit AudioMemo" tag:AMQuit menu:menu];
    self.item.menu = menu;

    EventTypeSpec events[] = {{kEventClassKeyboard, kEventHotKeyPressed}, {kEventClassKeyboard, kEventHotKeyReleased}};
    EventTargetRef target = GetApplicationEventTarget();
    EventHandlerRef handler = NULL;
    BOOL installed = InstallEventHandler(target, onHotKey, 2, events, (__bridge void *)self, &handler) == noErr;
    self.handler = handler;
    UInt32 keyCode = useF18 ? kVK_F18 : kVK_Space;
    UInt32 modifiers = useF18 ? 0 : (controlKey | optionKey);
    EventHotKeyRef hotKey = NULL, hotKeyBoth = NULL;
    BOOL registered = installed && RegisterEventHotKey(keyCode, modifiers, hotKeyID, target, 0, &hotKey) == noErr;
    BOOL registeredBoth = installed && RegisterEventHotKey(keyCode, modifiers | (useF18 ? optionKey : shiftKey),
        hotKeyBothID, target, 0, &hotKeyBoth) == noErr;
    self.hotKey = hotKey;
    self.hotKeyBoth = hotKeyBoth;
    NSString *shortcut = useF18 ? @"F18" : @"Control–Option–Space";
    NSString *shortcutBoth = useF18 ? @"Option–F18" : @"Control–Option–Shift–Space";
    NSMenuItem *keys = [self add:[NSString stringWithFormat:@"Quick Scribe: %@%@", shortcut,
        registered ? @"" : @" (unavailable)"] tag:-1 menu:menu];
    keys.enabled = NO;
    NSMenuItem *bothKeys = [self add:[NSString stringWithFormat:@"Us and Them: %@%@", shortcutBoth,
        registeredBoth ? @"" : @" (unavailable)"] tag:-1 menu:menu];
    bothKeys.enabled = NO;
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
    BOOL startDictate = action == AMStartDictate || action == AMStartDictateBoth;
    BOOL toggleDictate = action == AMToggle || action == AMToggleBoth;
    if ((action == AMStartMeeting || startDictate || toggleDictate) && !self.item.button.window.isVisible) return;
    AMTarget *prior = self.gestureTarget;
    if (startDictate || (toggleDictate && self.dictate.tag != AMStopDictate)) {
        self.gestureTarget = snapshot(YES);
        if (startDictate) self.gestureTarget.display = pointerDisplay();
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
    self.keysDown = 0;
    [self.panel orderOut:nil];
}
- (void)unlock:(NSNotification *)notice {
    (void)notice;
    self.locked = NO;
}
- (void)tick:(NSTimer *)timer {
    (void)timer;
    amGoTick(self.handle, (__bridge void *)self);
    if (self.trayIcon != AMTrayIdle) [self updateTrayIcon:self.trayIcon];
    if (self.panel.isVisible && ([self.caption.phase isEqualToString:@"starting"] ||
                                 [self.caption.phase isEqualToString:@"finalizing"])) self.caption.needsDisplay = YES;
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
    if (self.hotKeyBoth) UnregisterEventHotKey(self.hotKeyBoth);
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
                  int dictate, int meeting, int cancel, int icon) {
    AMDesktop *ui = (__bridge AMDesktop *)ptr;
    [ui updateTrayIcon:(AMTrayIcon)icon];
    ui.item.button.toolTip = [NSString stringWithFormat:@"AudioMemo — %@", text(title)];
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
    ui.dictateBoth.enabled = dictate == 1;
    ui.dictate.title = dictate == 2 ? @"Stop Quick Scribe" : @"Start Quick Scribe";
    ui.cancel.hidden = !cancel;
    ui.meeting.enabled = meeting != 0;
    ui.meeting.tag = meeting == 1 ? AMStartMeeting : meeting == 2 ? AMStopMeeting : -1;
    ui.meeting.title = meeting == 2 ? @"Stop Meeting" : @"Start Meeting";
    ui.chooseMeeting.enabled = meeting == 1;
}

static NSAttributedString *captionLine(NSString *done, NSString *draft, NSString *empty) {
    if (!done.length && !draft.length) draft = empty;
    NSDictionary *committed = @{NSFontAttributeName: [NSFont systemFontOfSize:17],
        NSForegroundColorAttributeName: NSColor.whiteColor};
    NSDictionary *tentative = @{NSFontAttributeName: [NSFont systemFontOfSize:17],
        NSForegroundColorAttributeName: [NSColor colorWithSRGBRed:0.76 green:0.79 blue:0.84 alpha:1]};
    NSMutableAttributedString *line = [[NSMutableAttributedString alloc] initWithString:done attributes:committed];
    if (draft.length) {
        if (done.length && ![done hasSuffix:@" "] && ![draft hasPrefix:@" "])
            [line appendAttributedString:[[NSAttributedString alloc] initWithString:@" " attributes:committed]];
        [line appendAttributedString:[[NSAttributedString alloc] initWithString:draft attributes:tentative]];
    }
    return line;
}

void am_ui_caption(void *ptr, const char *sessionID, const char *phase, const char *stable, const char *tentative,
    double level, int split, const char *us, const char *usDraft, double usLevel,
    const char *them, const char *themDraft, double themLevel) {
    AMDesktop *ui = (__bridge AMDesktop *)ptr;
    NSString *session = text(sessionID), *stage = text(phase);
    if (![ui active] || !session.length) {
        [ui.panel orderOut:nil];
        ui.caption.caption = nil;
        ui.caption.remoteCaption = nil;
        ui.captionSession = nil;
        return;
    }
    BOOL meeting = [stage isEqualToString:@"meeting"];
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
    BOOL recording = [stage isEqualToString:@"recording"];
    BOOL starting = [stage isEqualToString:@"starting"];
    BOOL refining = [stage isEqualToString:@"finalizing"];
    split = split && (recording || starting || refining);
    NSRect work = screen.visibleFrame;
    CGFloat width = MIN(split ? 560 : 720, NSWidth(work) - 40);
    if (width <= 32) return;
    CGFloat height = split ? 2 * captionCardHeight + captionCardGap : captionCardHeight;
    NSRect frame = NSMakeRect(NSMidX(work) - width / 2, NSMinY(work) + 18, width, height);
    if (!NSEqualRects(ui.panel.frame, frame)) [ui.panel setFrame:frame display:NO];
    NSString *empty = meeting || recording ? @"Listening…" : nil;
    if (split && starting) empty = @"Opening audio…";
    else if (split && refining) empty = @"Refining…";
    ui.caption.phase = stage;
    ui.caption.split = split != 0;
    ui.caption.level = split ? usLevel : level;
    ui.caption.remoteLevel = themLevel;
    ui.caption.caption = captionLine(text(split ? us : stable), text(split ? usDraft : tentative), empty);
    ui.caption.remoteCaption = split ? captionLine(text(them), text(themDraft), empty) : nil;
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
