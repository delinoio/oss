#import <CoreGraphics/CoreGraphics.h>
#import <Foundation/Foundation.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <math.h>
#import <stdint.h>
#import <stdlib.h>
#import <string.h>

typedef struct {
  uint8_t *bytes;
  size_t len;
  uint32_t width;
  uint32_t height;
  int32_t status;
} RealQAMacosBytes;

static const int32_t REALQA_SUCCESS = 0;
static const int32_t REALQA_PERMISSION_LOST = 1;
static const int32_t REALQA_CANCELLED = 2;
static const int32_t REALQA_PROTECTED_CONTENT = 3;
static const int32_t REALQA_SOURCE_LOST = 4;
static const int32_t REALQA_CAPTURE_FAILED = 5;
static const NSUInteger REALQA_MAX_PROCESS_NAME_UTF16_UNITS = 128;
static const NSUInteger REALQA_MAX_WINDOW_TITLE_UTF16_UNITS = 256;

static bool realqa_wait(dispatch_semaphore_t semaphore) {
  return dispatch_semaphore_wait(
             semaphore,
             dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC)) == 0;
}

static SCShareableContent *realqa_shareable_content(NSError **outError) {
  dispatch_semaphore_t semaphore = dispatch_semaphore_create(0);
  __block SCShareableContent *content = nil;
  __block NSError *error = nil;
  [SCShareableContent
      getShareableContentExcludingDesktopWindows:YES
                             onScreenWindowsOnly:NO
                                completionHandler:^(SCShareableContent *value,
                                                    NSError *valueError) {
                                  content = value;
                                  error = valueError;
                                  dispatch_semaphore_signal(semaphore);
                                }];
  if (!realqa_wait(semaphore)) {
    if (outError != NULL) {
      *outError = nil;
    }
    return nil;
  }
  if (outError != NULL) {
    *outError = error;
  }
  return content;
}

static NSDictionary *realqa_rect(CGRect frame) {
  return @{
    @"x" : @(frame.origin.x),
    @"y" : @(frame.origin.y),
    @"width" : @(frame.size.width),
    @"height" : @(frame.size.height),
  };
}

static NSString *realqa_safe_metadata(NSString *value,
                                      NSUInteger maximumUtf16Units) {
  if (value == nil || maximumUtf16Units == 0) {
    return nil;
  }
  NSCharacterSet *pathSeparators =
      [NSCharacterSet characterSetWithCharactersInString:@"/\\"];
  if ([value rangeOfCharacterFromSet:pathSeparators].location != NSNotFound ||
      [value rangeOfString:@"file:"
                   options:NSCaseInsensitiveSearch | NSAnchoredSearch]
              .location != NSNotFound) {
    return nil;
  }

  NSMutableString *sanitized =
      [NSMutableString stringWithCapacity:MIN(value.length,
                                               maximumUtf16Units)];
  NSCharacterSet *controlCharacters = NSCharacterSet.controlCharacterSet;
  for (NSUInteger index = 0; index < value.length;) {
    NSRange sequenceRange =
        [value rangeOfComposedCharacterSequenceAtIndex:index];
    index = NSMaxRange(sequenceRange);
    if (sequenceRange.length > maximumUtf16Units - sanitized.length) {
      break;
    }
    NSString *sequence = [value substringWithRange:sequenceRange];
    if ([sequence rangeOfCharacterFromSet:controlCharacters].location ==
        NSNotFound) {
      [sanitized appendString:sequence];
    }
  }
  NSString *trimmed = [sanitized
      stringByTrimmingCharactersInSet:NSCharacterSet
                                          .whitespaceAndNewlineCharacterSet];
  return trimmed.length == 0 ? nil : trimmed;
}

bool realqa_macos_preflight_permission(void) {
  return CGPreflightScreenCaptureAccess();
}

bool realqa_macos_request_permission(void) {
  return CGRequestScreenCaptureAccess();
}

static bool realqa_system_stopped_stream(NSError *error) {
  // This enum is absent from macOS 14 SDKs. Remove the compatibility guard when
  // macOS 15 is the minimum build SDK and deployment target.
#if __MAC_OS_X_VERSION_MAX_ALLOWED >= 150000
  if (@available(macOS 15.0, *)) {
    return error.code == SCStreamErrorSystemStoppedStream;
  }
#else
  (void)error;
#endif
  return false;
}

char *realqa_macos_copy_catalog_json(void) {
  @autoreleasepool {
    NSError *captureError = nil;
    SCShareableContent *content = realqa_shareable_content(&captureError);
    if (content == nil || captureError != nil) {
      return NULL;
    }

    NSMutableArray *displays = [NSMutableArray array];
    for (SCDisplay *display in content.displays) {
      [displays addObject:@{
        @"id" : @(display.displayID),
        @"frame" : realqa_rect(display.frame),
        // SCDisplay reports logical dimensions. Core Graphics supplies the backing
        // pixel dimensions needed by the shared mixed-scale geometry contract.
        @"width" : @(CGDisplayPixelsWide(display.displayID)),
        @"height" : @(CGDisplayPixelsHigh(display.displayID)),
        @"primary" : @(display.displayID == CGMainDisplayID()),
      }];
    }

    NSMutableArray *windows = [NSMutableArray array];
    for (SCWindow *window in content.windows) {
      if (window.windowLayer != 0 || window.owningApplication == nil) {
        continue;
      }
      NSString *processName = realqa_safe_metadata(
          window.owningApplication.applicationName,
          REALQA_MAX_PROCESS_NAME_UTF16_UNITS);
      NSString *title = realqa_safe_metadata(
          window.title, REALQA_MAX_WINDOW_TITLE_UTF16_UNITS);
      [windows addObject:@{
        @"id" : @(window.windowID),
        @"frame" : realqa_rect(window.frame),
        @"onScreen" : @(window.onScreen),
        @"processName" : processName == nil ? NSNull.null : processName,
        @"title" : title == nil ? NSNull.null : title,
      }];
    }

    NSDictionary *catalog = @{@"displays" : displays, @"windows" : windows};
    NSError *jsonError = nil;
    NSData *json = [NSJSONSerialization dataWithJSONObject:catalog
                                                   options:0
                                                     error:&jsonError];
    if (json == nil || jsonError != nil) {
      return NULL;
    }
    char *copy = malloc(json.length + 1);
    if (copy == NULL) {
      return NULL;
    }
    memcpy(copy, json.bytes, json.length);
    copy[json.length] = '\0';
    return copy;
  }
}

static int32_t realqa_error_status(NSError *error, int32_t sourceKind) {
  if (!CGPreflightScreenCaptureAccess()) {
    return REALQA_PERMISSION_LOST;
  }
  if ([error.domain isEqualToString:SCStreamErrorDomain]) {
    if (error.code == SCStreamErrorUserDeclined) {
      return REALQA_PERMISSION_LOST;
    }
    if (error.code == SCStreamErrorUserStopped ||
        realqa_system_stopped_stream(error)) {
      return REALQA_CANCELLED;
    }
    if (error.code == SCStreamErrorNoCaptureSource ||
        error.code == SCStreamErrorNoDisplayList ||
        error.code == SCStreamErrorNoWindowList ||
        error.code == SCStreamErrorFailedApplicationConnectionInvalid ||
        error.code == SCStreamErrorFailedApplicationConnectionInterrupted ||
        error.code == SCStreamErrorFailedNoMatchingApplicationContext) {
      return REALQA_SOURCE_LOST;
    }
  }
  (void)sourceKind;
  return REALQA_CAPTURE_FAILED;
}

static RealQAMacosBytes realqa_empty_result(int32_t status) {
  RealQAMacosBytes result = {NULL, 0, 0, 0, status};
  return result;
}

RealQAMacosBytes realqa_macos_capture(
    int32_t sourceKind, uint32_t sourceID, double sourceX, double sourceY,
    double sourceWidth, double sourceHeight, uint32_t outputWidth,
    uint32_t outputHeight, bool showsCursor) {
  @autoreleasepool {
    if (outputWidth == 0 || outputHeight == 0 ||
        ((uint64_t)outputWidth * (uint64_t)outputHeight) > 100000000ULL) {
      return realqa_empty_result(REALQA_CAPTURE_FAILED);
    }
    NSError *catalogError = nil;
    SCShareableContent *content = realqa_shareable_content(&catalogError);
    if (content == nil || catalogError != nil) {
      return realqa_empty_result(realqa_error_status(catalogError, sourceKind));
    }

    SCContentFilter *filter = nil;
    if (sourceKind == 0) {
      SCDisplay *selected = nil;
      for (SCDisplay *display in content.displays) {
        if (display.displayID == sourceID) {
          selected = display;
          break;
        }
      }
      if (selected == nil) {
        return realqa_empty_result(REALQA_SOURCE_LOST);
      }
      filter = [[SCContentFilter alloc] initWithDisplay:selected
                                      excludingWindows:@[]];
    } else if (sourceKind == 1) {
      SCWindow *selected = nil;
      for (SCWindow *window in content.windows) {
        if (window.windowID == sourceID && window.onScreen) {
          selected = window;
          break;
        }
      }
      if (selected == nil) {
        return realqa_empty_result(REALQA_SOURCE_LOST);
      }
      filter =
          [[SCContentFilter alloc] initWithDesktopIndependentWindow:selected];
    } else {
      return realqa_empty_result(REALQA_CAPTURE_FAILED);
    }

    SCStreamConfiguration *configuration = [[SCStreamConfiguration alloc] init];
    configuration.width = outputWidth;
    configuration.height = outputHeight;
    configuration.showsCursor = showsCursor;
    configuration.scalesToFit = YES;
    configuration.preservesAspectRatio = NO;
    configuration.shouldBeOpaque = NO;
    if (sourceKind == 0) {
      if (!isfinite(sourceX) || !isfinite(sourceY) || !isfinite(sourceWidth) ||
          !isfinite(sourceHeight) || sourceWidth <= 0 || sourceHeight <= 0) {
        return realqa_empty_result(REALQA_CAPTURE_FAILED);
      }
      configuration.sourceRect =
          CGRectMake(sourceX, sourceY, sourceWidth, sourceHeight);
    }

    dispatch_semaphore_t semaphore = dispatch_semaphore_create(0);
    NSObject *captureStateLock = [[NSObject alloc] init];
    __block CGImageRef image = NULL;
    __block NSError *captureError = nil;
    __block bool acceptsResult = true;
    [SCScreenshotManager
        captureImageWithFilter:filter
                 configuration:configuration
             completionHandler:^(CGImageRef value, NSError *valueError) {
               @synchronized(captureStateLock) {
                 if (acceptsResult) {
                   acceptsResult = false;
                   if (value != NULL) {
                     image = CGImageRetain(value);
                   }
                   captureError = valueError;
                 }
               }
               dispatch_semaphore_signal(semaphore);
             }];
    if (!realqa_wait(semaphore)) {
      @synchronized(captureStateLock) {
        acceptsResult = false;
        if (image != NULL) {
          CGImageRelease(image);
          image = NULL;
        }
      }
      return realqa_empty_result(REALQA_CAPTURE_FAILED);
    }
    if (image == NULL || captureError != nil) {
      if (image != NULL) {
        CGImageRelease(image);
      }
      return realqa_empty_result(realqa_error_status(captureError, sourceKind));
    }

    size_t byteLength = (size_t)outputWidth * (size_t)outputHeight * 4;
    uint8_t *bytes = calloc(byteLength, 1);
    CGColorSpaceRef colorSpace = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
    CGContextRef context = CGBitmapContextCreate(
        bytes, outputWidth, outputHeight, 8, (size_t)outputWidth * 4, colorSpace,
        kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
    if (bytes == NULL || colorSpace == NULL || context == NULL) {
      if (context != NULL) {
        CGContextRelease(context);
      }
      if (colorSpace != NULL) {
        CGColorSpaceRelease(colorSpace);
      }
      CGImageRelease(image);
      free(bytes);
      return realqa_empty_result(REALQA_CAPTURE_FAILED);
    }
    CGContextTranslateCTM(context, 0, outputHeight);
    CGContextScaleCTM(context, 1, -1);
    CGContextSetBlendMode(context, kCGBlendModeCopy);
    CGContextDrawImage(context, CGRectMake(0, 0, outputWidth, outputHeight),
                       image);
    CGContextRelease(context);
    CGColorSpaceRelease(colorSpace);
    CGImageRelease(image);

    bool hasVisiblePixel = false;
    for (size_t index = 3; index < byteLength; index += 4) {
      uint8_t alpha = bytes[index];
      if (alpha != 0) {
        hasVisiblePixel = true;
        if (alpha != UINT8_MAX) {
          for (size_t channel = index - 3; channel < index; channel++) {
            uint32_t straight =
                ((uint32_t)bytes[channel] * UINT8_MAX + alpha / 2) / alpha;
            bytes[channel] = (uint8_t)MIN(straight, UINT8_MAX);
          }
        }
      }
    }
    if (!hasVisiblePixel) {
      free(bytes);
      return realqa_empty_result(REALQA_PROTECTED_CONTENT);
    }
    RealQAMacosBytes result = {bytes, byteLength, outputWidth, outputHeight,
                               REALQA_SUCCESS};
    return result;
  }
}

void realqa_macos_free(void *pointer) {
  free(pointer);
}
