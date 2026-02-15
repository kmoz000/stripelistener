const std = @import("std");
const http = std.http;
const json = std.json;
const print = std.debug.print;

// Constants
const CLI_VERSION = "1.21.0";
const API_BASE = "https://api.stripe.com";
const SESSION_PATH = "/v1/stripecli/sessions";

pub const Config = struct {
    api_key: []const u8,
    device_name: []const u8 = "custom-stripe-listener",
    websocket_features: []const []const u8 = &[_][]const u8{"webhooks"},
};

pub const Session = struct {
    websocket_id: []const u8,
    websocket_url: []const u8,
    websocket_authorized_feature: []const u8,
};

// Response wrapper for JSON parsing
const SessionResponse = struct {
    websocket_id: []const u8,
    websocket_url: []const u8,
    websocket_authorized_feature: []const u8,
};

pub const StripeListener = struct {
    allocator: std.mem.Allocator,
    config: Config,
    session: ?Session,

    pub fn init(allocator: std.mem.Allocator, config: Config) StripeListener {
        return StripeListener{
            .allocator = allocator,
            .config = config,
            .session = null,
        };
    }

    pub fn authorize(self: *StripeListener) !void {
        var client = http.Client{ .allocator = self.allocator };
        defer client.deinit();

        // Construct URL
        const url_str = try std.fmt.allocPrint(self.allocator, "{s}{s}", .{ API_BASE, SESSION_PATH });
        defer self.allocator.free(url_str);
        
        const uri = try std.Uri.parse(url_str);

        // Build form body
        // Note: A real implementation would need a proper URL encoder. 
        // For simplicity in this example without deps, we assume simple values.
        var body = std.ArrayList(u8).init(self.allocator);
        defer body.deinit();
        
        try body.writer().print("device_name={s}", .{self.config.device_name});
        for (self.config.websocket_features) |feature| {
            try body.writer().print("&websocket_features[]={s}", .{feature});
        }

        var header_buf: [4096]u8 = undefined;
        var req = try client.open(.POST, uri, .{ .server_header_buffer = &header_buf });
        defer req.deinit();

        req.transfer_encoding = .content_length;
        
        // Headers
        try req.headers.append("Content-Type", "application/x-www-form-urlencoded");
        if (self.config.api_key.len > 0) {
            const auth_val = try std.fmt.allocPrint(self.allocator, "Bearer {s}", .{self.config.api_key});
            defer self.allocator.free(auth_val);
            try req.headers.append("Authorization", auth_val);
        }
        try req.headers.append("User-Agent", "Stripe/v1 stripe-cli/" ++ CLI_VERSION);
        
        // Send
        req.content_length = body.items.len;
        try req.send();
        try req.writeAll(body.items);
        try req.finish();
        try req.wait();

        if (req.response.status != .ok) {
            print("Authorize failed: {}\n", .{req.response.status});
            return error.AuthorizeFailed;
        }

        const resp_body = try req.reader().readAllAlloc(self.allocator, 1024 * 1024);
        defer self.allocator.free(resp_body);
        
        // Parse JSON
        // std.json in Zig 0.11/0.12/0.13 varies. Assuming recent dev or 0.11+.
        const parsed = try std.json.parseFromSlice(SessionResponse, self.allocator, resp_body, .{ .ignore_unknown_fields = true });
        defer parsed.deinit();

        // Deep copy strings for session since parsed.value relies on resp_body
        self.session = Session{
            .websocket_id = try self.allocator.dupe(u8, parsed.value.websocket_id),
            .websocket_url = try self.allocator.dupe(u8, parsed.value.websocket_url),
            .websocket_authorized_feature = try self.allocator.dupe(u8, parsed.value.websocket_authorized_feature),
        };

        print("Session created: ws_id={s}\n", .{self.session.?.websocket_id});
    }

    pub fn connect(self: *StripeListener) !void {
        if (self.session == null) {
            return error.NotAuthorized;
        }
        // WebSocket implementation requires a library or a significant amount of code for framing/handshake.
        // For this task, we will stub this out or leave a TODO since we cannot pull dependencies easily.
        
        print("Connecting to {s}...\n", .{self.session.?.websocket_url});
        print("TODO: Implement WebSocket handshake and framing using a library like 'zap' or 'websocket.zig'\n", .{});
    }
};

pub fn main() !void {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    var env_map = try std.process.getEnvMap(allocator);
    defer env_map.deinit();

    const api_key = env_map.get("STRIPE_API_KEY") orelse {
        print("Please set STRIPE_API_KEY environment variable\n", .{});
        return;
    };

    var listener = StripeListener.init(allocator, .{ .api_key = api_key });

    try listener.authorize();
    try listener.connect();
}
