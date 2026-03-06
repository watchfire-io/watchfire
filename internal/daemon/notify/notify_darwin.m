#import <UserNotifications/UserNotifications.h>
#import <Foundation/Foundation.h>

void SendDarwinNotification(const char *title, const char *message, const char *iconPath) {
    @autoreleasepool {
        UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];

        // Request authorization (no-op after first grant).
        [center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound)
                             completionHandler:^(BOOL granted, NSError *error) {}];

        UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
        content.title = [NSString stringWithUTF8String:title];
        content.body  = [NSString stringWithUTF8String:message];
        content.sound = [UNNotificationSound defaultSound];

        // Attach the icon image so it appears in the notification.
        if (iconPath) {
            NSString *path = [NSString stringWithUTF8String:iconPath];
            NSURL *fileURL = [NSURL fileURLWithPath:path];
            NSError *attachError = nil;
            UNNotificationAttachment *attachment =
                [UNNotificationAttachment attachmentWithIdentifier:@"icon"
                                                               URL:fileURL
                                                           options:nil
                                                             error:&attachError];
            if (attachment && !attachError) {
                content.attachments = @[attachment];
            }
        }

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
    }
}
