//go:build darwin && (webkit2_41 || desktop)

#import <Foundation/Foundation.h>
#import <CoreText/CoreText.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>

// Some Nerd Font patches add double-width symbols and lose the global mono
// trait even though their printable ASCII cells still have a fixed advance.
static BOOL terminal_fixed_pitch(CTFontRef font) {
    if ((CTFontGetSymbolicTraits(font) & kCTFontMonoSpaceTrait) != 0) return YES;
    enum { count = 95 };
    UniChar chars[count];
    CGGlyph glyphs[count];
    CGSize advances[count];
    for (int i = 0; i < count; i++) chars[i] = (UniChar)(32 + i);
    if (!CTFontGetGlyphsForCharacters(font, chars, glyphs, count)) return NO;
    CTFontGetAdvancesForGlyphs(font, kCTFontOrientationHorizontal, glyphs, advances, count);
    if (advances[0].width <= 0) return NO;
    for (int i = 1; i < count; i++) {
        if (fabs(advances[i].width - advances[0].width) > 0.01) return NO;
    }
    return YES;
}

// Inspect every face, not just a family's regular face: patched Nerd Font
// families can advertise monospace traits on only some of their styles.
// CoreText includes fonts installed for the current user and system fonts.
char *terminal_font_families(void) {
    @autoreleasepool {
        CTFontCollectionRef collection = CTFontCollectionCreateFromAvailableFonts(NULL);
        if (collection == NULL) return NULL;
        CFArrayRef descriptors = CTFontCollectionCreateMatchingFontDescriptors(collection);
        CFRelease(collection);
        if (descriptors == NULL) return NULL;
        NSMutableSet<NSString *> *families = [NSMutableSet set];
        for (id descriptor in (NSArray *)descriptors) {
            CTFontDescriptorRef desc = (CTFontDescriptorRef)descriptor;
            CTFontRef font = CTFontCreateWithFontDescriptor(desc, 0, NULL);
            if (font == NULL) continue;
            if (terminal_fixed_pitch(font)) {
                CFStringRef family = CTFontCopyFamilyName(font);
                if (family != NULL) {
                    [families addObject:(NSString *)family];
                    CFRelease(family);
                }
            }
            CFRelease(font);
        }
        CFRelease(descriptors);
        NSString *result = [[families allObjects] componentsJoinedByString:@"\n"];
        return strdup([result UTF8String]);
    }
}
