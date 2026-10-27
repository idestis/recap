import AppKit
import SwiftUI

/// Draws the loop mark at menu bar size. Geometry matches icons/recap-mark-*.svg
/// (viewBox 10 12 76 76): arc centered at 48,48 r28, arrowhead at the left end.
enum TrayIcon {
    /// SPEC D-23 REC red.
    static let rec = NSColor(srgbRed: 0xE5 / 255, green: 0x48 / 255, blue: 0x4D / 255, alpha: 1)

    static func image(recording: Bool, dot: Bool = true) -> NSImage {
        let image = NSImage(size: NSSize(width: 18, height: 18), flipped: true) { rect in
            let s = rect.width / 76
            func p(_ x: CGFloat, _ y: CGFloat) -> NSPoint { NSPoint(x: (x - 10) * s, y: (y - 12) * s) }

            // Template (idle) is drawn in black and tinted by macOS; when recording
            // the loop uses labelColor so it still matches light/dark menu bars.
            let ink: NSColor = recording ? .labelColor : .black
            ink.setStroke()

            let loop = NSBezierPath()
            loop.appendArc(withCenter: p(48, 48), radius: 28 * s, startAngle: 270, endAngle: 180, clockwise: false)
            loop.lineWidth = 8 * s
            loop.lineCapStyle = .round
            loop.stroke()

            let head = NSBezierPath()
            head.move(to: p(13, 51))
            head.line(to: p(20, 42))
            head.line(to: p(27, 51))
            head.lineWidth = 8 * s
            head.lineCapStyle = .round
            head.lineJoinStyle = .round
            head.stroke()

            if recording && dot {
                rec.setFill()
                NSBezierPath(ovalIn: NSRect(x: p(35, 35).x, y: p(35, 35).y, width: 26 * s, height: 26 * s)).fill()
            }
            return true
        }
        image.isTemplate = !recording
        image.accessibilityDescription = recording ? "recap recording" : "recap idle"
        return image
    }
}

/// Menu bar label. Idle: template loop. Recording: loop + red dot that blinks
/// while the session runs (steady with Reduce Motion) (SPEC D-23).
struct TrayLabel: View {
    let active: Bool
    @State private var dotOn = true
    private let blink = Timer.publish(every: 0.8, on: .main, in: .common).autoconnect()

    var body: some View {
        // .original keeps the red: menu bar labels otherwise render as a one-color template.
        Image(nsImage: TrayIcon.image(recording: active, dot: dotOn))
            .renderingMode(active ? .original : .template)
            .onReceive(blink) { _ in
                if active && !NSWorkspace.shared.accessibilityDisplayShouldReduceMotion {
                    dotOn.toggle()
                } else if !dotOn {
                    dotOn = true
                }
            }
    }
}

/// Native NSMenu (MenuBarExtra .menu style): Text rows are disabled info items,
/// Buttons are items, Menu is a submenu, inline Picker gives native checkmarks.
struct TrayMenu: View {
    @EnvironmentObject var model: StatusModel
    @EnvironmentObject var hotkey: HotKeyManager

    var body: some View {
        if model.status.active, let s = model.status.session {
            Text("● Recording · \(s.handle)" + (model.status.elapsed.map { " · \($0)" } ?? ""))
            Text(s.description.count > 60 ? String(s.description.prefix(57)) + "…" : s.description)
            Text(captured)
            Divider()
            Button("Add Context…") { OverlayController.shared.show() }
                .keyboardShortcut(hotkey.current.menuShortcut)
            Button("Stop and Review…") { StopFlow.run() }
        } else {
            Text("Idle")
            Button("Start Session…") { OverlayController.shared.show() }
                .keyboardShortcut(hotkey.current.menuShortcut)
        }
        Divider()
        if let spaces = model.spaces, !spaces.spaces.isEmpty {
            Menu("Space: \(spaces.current ?? "none")") {
                Picker("Space", selection: Binding(get: { spaces.current ?? "" }, set: { model.useSpace($0) })) {
                    ForEach(spaces.spaces, id: \.name) { Text($0.name).tag($0.name) }
                }
                .pickerStyle(.inline)
                .labelsHidden()
                Divider()
                Button("Open Recipe Repo") { openRepo() }
            }
            Divider()
        }
        Button("Settings…") { SettingsController.shared.show() }
            .keyboardShortcut(",", modifiers: .command)
        Button("Quit Recap") { NSApp.terminate(nil) }
            .keyboardShortcut("q", modifiers: .command)
    }

    private var captured: String {
        let c = model.status.counts ?? [:]
        return "Terminal \(c["terminal"] ?? 0) · Claude \(c["claude"] ?? 0) · Notes \((c["ctx"] ?? 0) + (c["overlay"] ?? 0))"
    }

    private func openRepo() {
        guard let repo = model.spaces?.currentSpace?.repo else { return }
        NSWorkspace.shared.open(URL(fileURLWithPath: (repo as NSString).expandingTildeInPath))
    }
}

// MARK: - Helpers

@MainActor
enum Alerts {
    static func show(_ title: String, _ text: String) {
        NSApp.activate(ignoringOtherApps: true)
        let a = NSAlert()
        a.messageText = title
        a.informativeText = String(text.prefix(1500))
        a.addButton(withTitle: "OK")
        a.runModal()
    }
}

@MainActor
enum CLIInstaller {
    static let link = "/usr/local/bin/recap"
    static var canInstall: Bool { FileManager.default.isExecutableFile(atPath: RecapCLI.bundledPath) }
    static var isInstalled: Bool {
        (try? FileManager.default.destinationOfSymbolicLink(atPath: link)) == RecapCLI.bundledPath
    }

    static func install() {
        guard canInstall, !isInstalled else { return }
        let escaped = RecapCLI.bundledPath.replacingOccurrences(of: "'", with: "'\\''")
        let cmd = "mkdir -p /usr/local/bin && ln -sf '\(escaped)' \(link)"
        let source = "do shell script \"\(cmd.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\""))\" with administrator privileges"
        var error: NSDictionary?
        NSApp.activate(ignoringOtherApps: true)
        NSAppleScript(source: source)?.executeAndReturnError(&error)
        if let error, (error[NSAppleScript.errorNumber] as? Int) != -128 { // -128 = user cancelled
            Alerts.show("Could not install", (error[NSAppleScript.errorMessage] as? String) ?? "Unknown error")
        }
    }
}

/// One question, then the CLI drafts (SPEC D-10). Shows the draft path and offers Publish.
@MainActor
enum StopFlow {
    static func run() {
        let handle = StatusModel.shared.status.session?.handle
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = "Did this fix it?"
        alert.informativeText = "A fix becomes a draft recipe in your repo. Nothing is committed until you apply it."
        alert.addButton(withTitle: "Yes, draft recipe")
        alert.addButton(withTitle: "No")
        alert.addButton(withTitle: "Cancel")
        let flag: String
        switch alert.runModal() {
        case .alertFirstButtonReturn: flag = "--fixed"
        case .alertSecondButtonReturn: flag = "--not-fixed"
        default: return
        }
        Task {
            let r = await RecapCLI.runAsync(["stop", flag, "--yes", "--json"])
            StatusModel.shared.refresh()
            report(r, handle: handle)
        }
    }

    private static func report(_ r: CLIResult, handle: String?) {
        guard r.ok else {
            Alerts.show("Stop failed", r.err.isEmpty ? r.out : r.err)
            return
        }
        let json = (try? JSONSerialization.jsonObject(with: Data(r.out.utf8))) as? [String: Any]
        let draft = find(json, ["draft_path", "draft", "recipe_path", "path", "recipe"])
        let published = find(json, ["handle", "session"]) ?? handle
        let alert = NSAlert()
        alert.messageText = draft == nil ? "Session stopped" : "Draft saved"
        alert.informativeText = draft ?? (r.out.isEmpty ? "No draft was created." : String(r.out.prefix(800)))
        alert.addButton(withTitle: "OK")
        if draft != nil { alert.addButton(withTitle: "Show in Finder") }
        if draft != nil, published != nil { alert.addButton(withTitle: "Publish") }
        NSApp.activate(ignoringOtherApps: true)
        switch alert.runModal() {
        case .alertSecondButtonReturn:
            if let draft { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: (draft as NSString).expandingTildeInPath)]) }
        case .alertThirdButtonReturn:
            if let published { publish(published) }
        default: break
        }
    }

    private static func find(_ json: [String: Any]?, _ keys: [String]) -> String? {
        guard let json else { return nil }
        for k in keys { if let v = json[k] as? String, !v.isEmpty { return v } }
        return nil
    }

    static func publish(_ handle: String) {
        Task {
            let r = await RecapCLI.runAsync(["publish", handle, "--yes"])
            let text = [r.out, r.err].filter { !$0.isEmpty }.joined(separator: "\n")
            Alerts.show(r.ok ? "Published" : "Publish failed", text.isEmpty ? (r.ok ? "Done." : "Exit code \(r.code)") : text)
        }
    }
}
