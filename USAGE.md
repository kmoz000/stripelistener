# Using `stripelistener` in Your Project

This repository contains the `stripelistener` library implemented in multiple languages. You can import these packages directly into your project without waiting for a package registry release.

## Go

Use `go get` to add the dependency:

```bash
go get github.com/kmoz000/stripelistener/go
```

**Usage:**

```go
import "github.com/kmoz000/stripelistener/go"
```

## TypeScript / Node.js

You can install directly from GitHub using `npm`, `yarn`, or `pnpm`.

**npm:**
```bash
npm install github:kmoz000/stripelistener#main --workspace=typescript
# Note: You might need to point to the specific subdirectory if supported, or install the root and import.
# A more robust way for subdirectories is often using a git submodule or a monorepo tool, 
# but for direct git dependencies, you may need to specify the path if the package.json is not at root.
```

*Alternative (if `package.json` is in `typescript/`)*:
If you cannot install a subdirectory directly via npm, you can use:
```bash
npm install https://github.com/kmoz000/stripelistener/tarball/main
```
*However, npm doesn't natively support installing from a subdirectory of a git repo easily without a build step or workspace setup.*

**Recommended for this repo structure:**
1.  **Clone/Submodule**: Add this repo as a submodule.
    ```bash
    git submodule add https://github.com/kmoz000/stripelistener.git deps/stripelistener
    ```
2.  **Local Path**:
    ```json
    "dependencies": {
      "stripelistener": "file:./deps/stripelistener/typescript"
    }
    ```

## Rust

Add the library as a Git dependency in your `Cargo.toml`. You can specify the `rust` subdirectory path implies the crate root is there, but Cargo handles git repos by searching for the workspace or crate.

**Cargo.toml:**

```toml
[dependencies]
stripelistener = { git = "https://github.com/kmoz000/stripelistener.git", branch = "main" }
```

*Note: Since the `Cargo.toml` is inside the `rust/` directory, you might need to specify it if it's not detected automatically, but Cargo is usually smart enough to find the package if you specify the package name matching the `Cargo.toml`.*

## Zig

To use the Zig package, you can use `build.zig.zon` (Zig 0.11+) to fetch it as a dependency.

**1. Initialize your project (`zig init-exe`)**

**2. Fetch the dependency:**

Run the following command to fetch the package and add it to your `build.zig.zon` (replace URL with actual repo URL):

```bash
zig fetch --save git+https://github.com/kmoz000/stripelistener.git#main
```

**3. Update `build.zig`:**

In your `build.zig`, add the dependency module:

```zig
const stripelistener_dep = b.dependency("stripelistener", .{
    .target = target,
    .optimize = optimize,
});
exe.root_module.addImport("stripelistener", stripelistener_dep.module("stripelistener"));
```

*Note: Since our `build.zig` is in a subdirectory (`zig/`), `zig fetch` at the root might not find it as expected immediately if the root is not a zig package. You likely need to use the `hash` from `zig fetch` manually or ensure the repo root has a `build.zig.zon` that points to `zig/`.*

**Alternative (Submodule):**
```bash
git submodule add https://github.com/kmoz000/stripelistener.git libs/stripelistener
```

In `build.zig`:
```zig
exe.addAnonymousModule("stripelistener", .{
    .source_file = .{ .path = "libs/stripelistener/zig/src/root.zig" },
});
```
