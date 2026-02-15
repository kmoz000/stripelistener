const std = @import("std");
pub const StripeListener = @import("main.zig").StripeListener;
pub const Config = @import("main.zig").Config;

test "basic test" {
    try std.testing.expect(true);
}
