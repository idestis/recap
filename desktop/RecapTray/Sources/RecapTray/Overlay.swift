import AppKit
import SwiftUI
import UniformTypeIdentifiers

// MARK: - Panel

/// Borderless floating panel that can take keyboard focus.
final class KeyPanel: NSPanel {
    override var canBecomeKey: Bool { true }
}

@MainActor
final class OverlayController {
    static let shared = OverlayController()
    let state = OverlayState()
    private var panel: KeyPanel?

    func toggle() {
        if panel?.isVisible == true { hide() } else { show() }
    }

    func show() {
        // Remember the app in front *before* we take focus: it decides the actions.
        state.prepare(front: NSWorkspace.shared.frontmostApplication)
        if panel == nil {
            let m = ShadowInset.size
            let p = KeyPanel(contentRect: NSRect(x: 0, y: 0, width: OverlayView.width + 2 * m, height: OverlayView.height + 2 * m),
                             styleMask: [.borderless, .fullSizeContentView],
                             backing: .buffered, defer: false)
            p.isFloatingPanel = true
            p.level = .floating
            p.backgroundColor = .clear
            p.isOpaque = false
            p.hasShadow = false
            p.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
            p.contentView = NSHostingView(rootView: OverlayView()
                .environmentObject(state)
                .environmentObject(StatusModel.shared)
                .environmentObject(HotKeyManager.shared))
            panel = p
        }
        guard let panel, let screen = NSScreen.main else { return }
        let frame = screen.visibleFrame
        // The transparent shadow margin sits below the bar too, so offset by it.
        panel.setFrameOrigin(NSPoint(x: frame.midX - panel.frame.width / 2, y: frame.minY + 60 - ShadowInset.size))
        NSApp.activate(ignoringOtherApps: true)
        panel.makeKeyAndOrderFront(nil)
        // Focus the field right away so dictation (macOS / Wispr Flow) lands in it.
        state.focusTick += 1
    }

    func hide() {
        panel?.orderOut(nil)
    }
}

// MARK: - Contextual sources

enum Browser: String {
    case safari = "com.apple.Safari"
    case chrome = "com.google.Chrome"
    case arc = "company.thebrowser.Browser"
    case brave = "com.brave.Browser"
    case edge = "com.microsoft.edgemac"

    var name: String {
        switch self {
        case .safari: "Safari"
        case .chrome: "Chrome"
        case .arc: "Arc"
        case .brave: "Brave"
        case .edge: "Edge"
        }
    }

    /// Needs the Automation permission once per browser (SPEC D-22).
    var script: String {
        switch self {
        case .safari: "tell application id \"\(rawValue)\" to return URL of front document"
        default: "tell application id \"\(rawValue)\" to return URL of active tab of front window"
        }
    }
}

enum Capture {
    static func tempURL(_ kind: String) -> URL {
        FileManager.default.temporaryDirectory
            .appendingPathComponent("recap-\(Int(Date().timeIntervalSince1970))-\(kind).png")
    }

    /// Front window of the app that was active before the overlay opened.
    /// Needs the Screen Recording permission (asked on first use).
    static func window(of pid: pid_t) -> URL? {
        guard let list = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID)
            as? [[String: Any]],
            let win = list.first(where: {
                ($0[kCGWindowOwnerPID as String] as? pid_t) == pid && ($0[kCGWindowLayer as String] as? Int) == 0
            }),
            let id = win[kCGWindowNumber as String] as? Int
        else { return nil }
        let url = tempURL("window")
        return screencapture(["-x", "-o", "-l", String(id), url.path]) ? url : nil
    }

    /// Interactive drag-to-select, like ⌘⇧4.
    static func area() -> URL? {
        let url = tempURL("area")
        _ = screencapture(["-i", "-x", url.path])
        return FileManager.default.fileExists(atPath: url.path) ? url : nil
    }

    static func tabURL(_ browser: Browser) -> String? {
        var error: NSDictionary?
        return NSAppleScript(source: browser.script)?.executeAndReturnError(&error).stringValue
    }

    private static func screencapture(_ args: [String]) -> Bool {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
        p.arguments = args
        do { try p.run() } catch { return false }
        p.waitUntilExit()
        return p.terminationStatus == 0
    }
}

// MARK: - State

@MainActor
final class OverlayState: ObservableObject {
    @Published var text = ""
    @Published var attachments: [URL] = []
    @Published var frontAppName = "window"
    @Published var browser: Browser?
    @Published var busy = false
    @Published var focusTick = 0
    private var frontPID: pid_t = 0

    func prepare(front: NSRunningApplication?) {
        guard let front, front.bundleIdentifier != Bundle.main.bundleIdentifier else { return }
        frontPID = front.processIdentifier
        frontAppName = front.localizedName ?? "window"
        browser = front.bundleIdentifier.flatMap(Browser.init(rawValue:))
    }

    /// Quiet "Detected:" line. The CLI does the real detection on `ctx`.
    var detected: [String] {
        var out: [String] = []
        let lower = text.lowercased()
        if lower.contains("grafana") || lower.contains("datadog") { out.append("dashboard") }
        if lower.contains("slack.com/archives") { out.append("Slack channel") }
        if lower.contains("/pull/") { out.append("pull request") }
        let decision = ["ruled out", "decided", "do not", "don't", "won't", "no rollback"]
        if decision.contains(where: { lower.contains($0) }) { out.append("decision") }
        return out
    }

    func screenshot() {
        let pid = frontPID
        OverlayController.shared.hide()
        Task.detached {
            let url = Capture.window(of: pid)
            await MainActor.run {
                if let url { self.attachments.append(url) }
                OverlayController.shared.show()
            }
        }
    }

    func area() {
        OverlayController.shared.hide()
        Task.detached {
            let url = Capture.area()
            await MainActor.run {
                if let url { self.attachments.append(url) }
                OverlayController.shared.show()
            }
        }
    }

    func tabLink(_ browser: Browser) {
        if let link = Capture.tabURL(browser) {
            text = text.isEmpty ? link : text + " " + link
        }
    }

    func pickFile() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = true
        if panel.runModal() == .OK { attachments.append(contentsOf: panel.urls) }
    }

    func drop(_ providers: [NSItemProvider]) -> Bool {
        for provider in providers {
            _ = provider.loadObject(ofClass: URL.self) { url, _ in
                guard let url else { return }
                Task { @MainActor in self.attachments.append(url) }
            }
        }
        return true
    }

    /// Sends text and attachments to the CLI. With no session running, the
    /// text starts one (it becomes the description).
    func send(andStop: Bool = false) {
        let body = text.trimmingCharacters(in: .whitespacesAndNewlines)
        let files = attachments
        guard !body.isEmpty || !files.isEmpty, !busy else { return }
        busy = true
        let active = StatusModel.shared.status.active
        let space = StatusModel.shared.currentSpaceName
        Task {
            var failure: String?
            if !active {
                var args = ["start", body.isEmpty ? "Untitled incident" : body]
                if let space { args += ["--space", space] }
                let r = await RecapCLI.runAsync(args)
                if !r.ok { failure = r.err.isEmpty ? r.out : r.err }
            } else if !body.isEmpty {
                let r = await RecapCLI.runAsync(["ctx", "--source", "overlay", "-"], stdin: body)
                if !r.ok { failure = r.err.isEmpty ? r.out : r.err }
            }
            if failure == nil {
                for file in files {
                    let r = await RecapCLI.runAsync(["ctx", "--source", "overlay", file.path])
                    if !r.ok { failure = r.err.isEmpty ? r.out : r.err }
                }
            }
            busy = false
            StatusModel.shared.refresh()
            if let failure {
                OverlayController.shared.hide()
                Alerts.show("recap failed", failure)
                return
            }
            text = ""
            attachments = []
            OverlayController.shared.hide()
            if andStop {
                StatusModel.shared.status.active = true // session was just started or already running
                StopFlow.run()
            }
        }
    }

    /// Mic button: focus the field, then ask macOS to start Dictation.
    func dictate() {
        focusTick += 1
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) {
            NSApp.sendAction(Selector(("startDictation:")), to: nil, from: nil)
        }
    }
}

// MARK: - Views

private let barRadius: CGFloat = 20
private let pillRadius: CGFloat = 12
private let innerRadius: CGFloat = 10

private extension View {
    /// Overlay glass: shared look plus a deeper shadow, since the bar floats over any app.
    func glass(_ radius: CGFloat) -> some View { glassPanel(radius: radius, elevated: true) }
}

struct OverlayView: View {
    @EnvironmentObject var state: OverlayState
    @EnvironmentObject var model: StatusModel
    @EnvironmentObject var hotkey: HotKeyManager
    @FocusState private var focused: Bool
    static let width: CGFloat = 780
    static let height: CGFloat = 190

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Spacer(minLength: 0)
            HStack {
                Spacer()
                if model.status.active { SessionChip() } else { SpaceChip() }
            }
            bar
            actions.padding(.top, 2)
        }
        .frame(width: Self.width, height: Self.height, alignment: .bottom)
        .padding(ShadowInset.size)
        .onAppear { focus() }
        .onChange(of: state.focusTick) { _, _ in focus() }
        .onExitCommand { OverlayController.shared.hide() }
        .background {
            // ⌘Return: add and stop.
            Button("") { state.send(andStop: true) }
                .keyboardShortcut(.return, modifiers: .command)
                .hidden()
        }
    }

    private func focus() {
        focused = true
        DispatchQueue.main.async { focused = true }
    }

    private var placeholder: String {
        model.status.active ? "Add context: a note, a link, anything" : "What’s wrong? This starts a session"
    }

    private var bar: some View {
        VStack(alignment: .leading, spacing: 8) {
            if !state.attachments.isEmpty || !state.detected.isEmpty {
                HStack(spacing: 10) {
                    ForEach(state.attachments, id: \.self) { url in
                        HStack(spacing: 6) {
                            Image(systemName: url.pathExtension == "png" ? "photo" : "doc")
                            Text(url.lastPathComponent).lineLimit(1)
                            Button { state.attachments.removeAll { $0 == url } } label: { Image(systemName: "xmark") }
                                .buttonStyle(.plain)
                                .help("Remove")
                        }
                        .font(.system(size: 12))
                        .padding(.horizontal, 8).frame(height: 26)
                        .background(.white.opacity(0.08), in: RoundedRectangle(cornerRadius: innerRadius, style: .continuous))
                    }
                    if !state.detected.isEmpty {
                        Text("Detected: " + state.detected.joined(separator: " · "))
                            .font(.system(size: 12)).foregroundStyle(.secondary)
                    }
                }
            }

            HStack(spacing: 10) {
                Button { state.pickFile() } label: {
                    Image(systemName: "plus").frame(width: 34, height: 34)
                }
                .buttonStyle(.plain)
                .overlay(RoundedRectangle(cornerRadius: innerRadius, style: .continuous).strokeBorder(.white.opacity(0.18)))
                .help("Attach or drop files")

                TextField(placeholder, text: $state.text)
                    .textFieldStyle(.plain)
                    .font(.system(size: 17))
                    .focused($focused)
                    .onSubmit { state.send() }

                Button { state.dictate() } label: {
                    Image(systemName: "mic").foregroundStyle(.secondary).frame(width: 30, height: 34)
                }
                .buttonStyle(.plain)
                .help("Dictate (or use Wispr Flow)")

                Button { state.send() } label: {
                    Image(systemName: state.busy ? "hourglass" : "arrow.up")
                        .font(.system(size: 14, weight: .bold))
                        .foregroundStyle(.black)
                        .frame(width: 34, height: 34)
                        .background(Brand.sunrise, in: RoundedRectangle(cornerRadius: innerRadius, style: .continuous))
                }
                .buttonStyle(.plain)
                .help(model.status.active ? "Add to session (Return, ⌘Return adds and stops)" : "Start session (Return)")
            }
        }
        .padding(EdgeInsets(top: 10, leading: 12, bottom: 10, trailing: 12))
        .glass(barRadius)
        .onDrop(of: [.fileURL], isTargeted: nil) { state.drop($0) }
    }

    /// Contextual actions: only what makes sense for the app that was in front.
    private var actions: some View {
        HStack(spacing: 8) {
            Pill(icon: "camera", title: "Screenshot of \(state.frontAppName)", key: "s") { state.screenshot() }
            Pill(icon: "rectangle.dashed", title: "Area", key: "a") { state.area() }
            if let browser = state.browser {
                Pill(icon: "link", title: "\(browser.name) tab link", key: "l") { state.tabLink(browser) }
            }
        }
    }
}

/// Above the bar, right-aligned: the session this context goes to. One line;
/// long names truncate in the middle instead of wrapping.
struct SessionChip: View {
    @EnvironmentObject var model: StatusModel

    var body: some View {
        if let s = model.status.session {
            HStack(spacing: 7) {
                Circle().fill(Color(nsColor: TrayIcon.rec)).frame(width: 7, height: 7)
                Text(s.handle).font(.system(size: 12, design: .monospaced))
                Text("·").foregroundStyle(.tertiary)
                Text(s.name).lineLimit(1).truncationMode(.middle)
                if let e = model.status.elapsed {
                    Text("·").foregroundStyle(.tertiary)
                    Text(e).monospacedDigit().foregroundStyle(.secondary)
                }
            }
            .font(.system(size: 12))
            .padding(.horizontal, 10).frame(height: 26)
            .frame(maxWidth: 460, alignment: .trailing)
            .fixedSize(horizontal: true, vertical: false)
            .glass(pillRadius)
            .help(s.description)
        }
    }
}

/// Idle: which space the new session goes to. Hidden if the CLI has no spaces.
struct SpaceChip: View {
    @EnvironmentObject var model: StatusModel

    var body: some View {
        if let spaces = model.spaces, !spaces.spaces.isEmpty {
            HStack(spacing: 6) {
                Text("New session in").foregroundStyle(.secondary)
                Menu {
                    ForEach(spaces.spaces, id: \.name) { sp in
                        Button(sp.name + (sp.name == spaces.current ? "  ✓" : "")) { model.useSpace(sp.name) }
                    }
                } label: {
                    HStack(spacing: 4) {
                        Text(spaces.current ?? "space")
                        Image(systemName: "chevron.down").font(.system(size: 9, weight: .semibold))
                    }
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
            }
            .font(.system(size: 12))
            .padding(.horizontal, 10).frame(height: 26)
            .glass(pillRadius)
            .help("Space for the new session")
        }
    }
}

struct Pill: View {
    let icon: String
    let title: String
    let key: Character
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 7) {
                Image(systemName: icon)
                Text(title)
                Text("⌘⇧\(String(key).uppercased())")
                    .font(.system(size: 11, design: .monospaced))
                    .padding(.horizontal, 5).padding(.vertical, 1)
                    .background(.white.opacity(0.1), in: RoundedRectangle(cornerRadius: 4))
            }
            .font(.system(size: 13))
            .padding(.horizontal, 12).frame(height: 34)
            .glass(pillRadius)
        }
        .buttonStyle(.plain)
        .keyboardShortcut(KeyEquivalent(key), modifiers: [.command, .shift])
    }
}

enum Brand {
    /// Sunrise: amber → coral (SPEC D-23).
    static let sunrise = LinearGradient(colors: [Color(red: 1.0, green: 0.745, blue: 0.333),
                                                 Color(red: 1.0, green: 0.353, blue: 0.278)],
                                        startPoint: .topLeading, endPoint: .bottomTrailing)
    static let amber = Color(red: 0.949, green: 0.702, blue: 0.239)
}
