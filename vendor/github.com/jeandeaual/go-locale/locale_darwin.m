//go:build darwin && !ios

#import <Foundation/Foundation.h>

const char *preferredLocalization()
{
    NSString *locale = [[[NSBundle mainBundle] preferredLocalizations] firstObject];

    return [locale UTF8String];
}

const char *preferredLocalizations()
{
    NSString *locales = [[[NSBundle mainBundle] preferredLocalizations] componentsJoinedByString:@","];

    return [locales UTF8String];
}
