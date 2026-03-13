#import <UserNotifications/UserNotifications.h>
#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>

void SetDarwinAppIcon(const void *data, int len) {
    @autoreleasepool {
        @try {
            NSData *imgData = [NSData dataWithBytes:data length:len];
            NSImage *icon = [[NSImage alloc] initWithData:imgData];
            if (icon) {
                [NSApp setApplicationIconImage:icon];
            }
        } @catch (NSException *exception) {
            NSLog(@"Watchfire: set app icon failed: %@", exception.reason);
        }
    }
}

// Returns YES if the process has a valid app bundle (required for UNUserNotificationCenter).
static BOOL hasAppBundle(void) {
    NSBundle *bundle = [NSBundle mainBundle];
    return bundle != nil && [bundle bundleIdentifier] != nil;
}

void SendDarwinNotification(const char *title, const char *message) {
    @autoreleasepool {
        @try {
            if (!hasAppBundle()) {
                NSLog(@"Watchfire: skipping notification (no app bundle)");
                return;
            }

            UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];

            // Request authorization (no-op after first grant).
            [center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound)
                                 completionHandler:^(BOOL granted, NSError *error) {}];

            UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
            content.title = [NSString stringWithUTF8String:title];
            content.body  = [NSString stringWithUTF8String:message];
            content.sound = [UNNotificationSound defaultSound];

            NSString *identifier = [[NSUUID UUID] UUIDString];
            UNNotificationRequest *request =
                [UNNotificationRequest requestWithIdentifier:identifier
                                                     content:content
                                                     trigger:nil];

            [center addNotificationRequest:request withCompletionHandler:^(NSError *error) {
                if (error) {
                    NSLog(@"Watchfire notification error: %@", error);
                }
            }];
        } @catch (NSException *exception) {
            NSLog(@"Watchfire: notification failed: %@", exception.reason);
        }
    }
}
