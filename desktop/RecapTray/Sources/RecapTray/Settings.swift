import AppKit
import Carbon.HIToolbox
import ServiceManagement
import SwiftUI

// MARK: - Model

struct ModelInfo: Sendable {
    var provider = ""
    var name = ""
    var baseURL = ""
    var enabled = false
}

@MainActor
final class SettingsModel: ObservableObject {
    @Published var recording = false
    @Published var launchAtLogin = SMAppService.mainApp.status == .enabled
    @Published var cliInstalled = CLIInstaller.isInstalled
    @Published var model = ModelInfo()

    func refresh() {
        launchAtLogin = SMAppService.mainApp.status == .enabled
        cliInstalled = CLIInstaller.isInstalled
        StatusModel.shared.refreshSpaces()
        Task {
            let r = await RecapCLI.runAsync(["models", "--json"])
            guard r.ok, let json = (try? JSONSerialization.jsonObject(with: Data(r.out.utf8))) as? [String: Any] else { return }
            let m = json["model"] as? [String: Any] ?? [:]
            model = ModelInfo(provider: m["provider"] as? String ?? "", name: m["model"] as? String ?? "",
                              baseURL: m["base_url"] as? String ?? "", enabled: json["enabled"] as? Bool ?? false)
        }
    }

    func toggleLaunchAtLogin() {
        do {
            if launchAtLogin { try SMAppService.mainApp.unregister() } else { try SMAppService.mainApp.register() }
        } catch {
            Alerts.show("Launch at login failed", error.localizedDescription)
        }
        launchAtLogin = SMAppService.mainApp.status == .enabled
    }

    func installCLI() {
        CLIInstaller.install()
        cliInstalled = CLIInstaller.isInstalled
    }

    func startRecording() {
        recording = true
        HotKeyManager.shared.pause()
    }

    func cancelRecording() {
        guard recording else { return }
        recording = false
        HotKeyManager.shared.register()
    }

    /// Esc cancels, ⌫ restores the default, anything with ⌘/⌥/⌃ becomes the shortcut.
    func record(_ e: NSEvent) {
        switch Int(e.keyCode) {
        case kVK_Escape:
            cancelRecording()
        case kVK_Delete, kVK_ForwardDelete:
            recording = false
            HotKeyManager.shared.set(.default)
        default:
            let s = Shortcut(event: e)
            guard s.isValid else { NSSound.beep(); return }
            recording = false
            HotKeyManager.shared.set(s)
        }
    }
}

// MARK: - Panel

/// Settings live in a borderless glass panel centered on the active screen.
/// It closes on Esc, ⌘W or when it loses focus (unless an alert is up).
@MainActor
final class SettingsController: NSObject, NSWindowDelegate {
    static let shared = SettingsController()
    static let size = CGSize(width: 500, height: 540)
    private let model = SettingsModel()
    private var panel: KeyPanel?
    private var monitor: Any?

    func show() {
        // Close the tray popover first so the panel isn't hidden behind it.
        NSApp.windows.filter { $0.className.contains("MenuBarExtra") }.forEach { $0.orderOut(nil) }
        model.refresh()
        let p = panel ?? makePanel()
        panel = p
        p.contentView = NSHostingView(rootView: SettingsView()
            .environmentObject(model)
            .environmentObject(StatusModel.shared)
            .environmentObject(HotKeyManager.shared))
        let screen = NSScreen.screens.first { NSMouseInRect(NSEvent.mouseLocation, $0.frame, false) } ?? NSScreen.main
        if let f = screen?.visibleFrame {
            let m = ShadowInset.size
            let w = Self.size.width + 2 * m
            let h = min(Self.size.height, f.height - 40) + 2 * m
            p.setFrame(NSRect(x: f.midX - w / 2, y: f.midY - h / 2 + f.height * 0.05, width: w, height: h), display: true)
        }
        NSApp.activate(ignoringOtherApps: true)
        p.makeKeyAndOrderFront(nil)
        if monitor == nil {
            monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] e in
                guard let self, self.panel?.isKeyWindow == true else { return e }
                if self.model.recording { self.model.record(e); return nil }
                if Int(e.keyCode) == kVK_Escape { self.hide(); return nil }
                if e.modifierFlags.contains(.command), e.charactersIgnoringModifiers == "w" { self.hide(); return nil }
                return e
            }
        }
    }

    func hide() {
        model.cancelRecording()
        if let monitor { NSEvent.removeMonitor(monitor) }
        monitor = nil
        panel?.orderOut(nil)
        DispatchQueue.main.async { [weak self] in
            if self?.panel?.isVisible == false { self?.panel?.contentView = nil }
        }
    }

    func windowDidResignKey(_ notification: Notification) {
        // Alerts (e.g. the admin prompt for the CLI link) take key briefly; stay open for those.
        guard NSApp.modalWindow == nil else { return }
        hide()
    }

    private func makePanel() -> KeyPanel {
        let p = KeyPanel(contentRect: NSRect(origin: .zero, size: Self.size),
                         styleMask: [.borderless, .fullSizeContentView],
                         backing: .buffered, defer: true)
        p.isFloatingPanel = true
        p.level = .floating
        p.backgroundColor = .clear
        p.isOpaque = false
        p.hasShadow = false
        p.hidesOnDeactivate = false
        p.isReleasedWhenClosed = false
        p.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .transient, .ignoresCycle]
        p.delegate = self
        return p
    }
}

// MARK: - Views

struct SettingsView: View {
    @EnvironmentObject var settings: SettingsModel
    @EnvironmentObject var status: StatusModel
    @EnvironmentObject var hotkey: HotKeyManager

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header.padding(.bottom, 14)
            ScrollView {
                VStack(alignment: .leading, spacing: 2) {
                    section("General", first: true)
                    row("Launch at login", action: { settings.toggleLaunchAtLogin() }) {
                        Toggle("", isOn: Binding(get: { settings.launchAtLogin }, set: { _ in settings.toggleLaunchAtLogin() }))
                            .toggleStyle(.switch).controlSize(.small).labelsHidden()
                    }
                    row("Command line tool", action: canInstallCLI ? { settings.installCLI() } : nil) {
                        if settings.cliInstalled {
                            value(CLIInstaller.link)
                        } else if CLIInstaller.canInstall {
                            value("Install…", accent: true)
                        } else {
                            value("Not bundled")
                        }
                    }

                    section("Shortcut")
                    row("Quick context", action: { settings.recording ? settings.cancelRecording() : settings.startRecording() }) {
                        if settings.recording {
                            value("Press keys…", accent: true)
                        } else {
                            KeyLabel(keys: hotkey.current.display)
                        }
                    }
                    if hotkey.registrationFailed && !settings.recording {
                        hint("This shortcut is taken by another app. Click to record a different one.")
                    } else {
                        hint(settings.recording ? "Esc cancels · ⌫ restores ⌥⇧Space · needs ⌘, ⌥ or ⌃"
                                                : "Opens the quick context bar from any app. Dictation and Wispr Flow work inside it.")
                    }

                    section("Spaces")
                    if let spaces = status.spaces, !spaces.spaces.isEmpty {
                        ForEach(spaces.spaces, id: \.name) { sp in
                            row(sp.name, selected: sp.name == spaces.current, action: { status.useSpace(sp.name) }) {
                                HStack(spacing: 8) {
                                    value(publishLabel(sp.publish))
                                    Button { open(sp.repo) } label: { Image(systemName: "folder") }
                                        .buttonStyle(.borderless).help(sp.repo ?? "")
                                }
                            }
                        }
                        hint("Click a space to make it the default. A space is also picked by the folder you work in.")
                    } else {
                        hint("No spaces yet. In a terminal: cd <recipe repo> && recap init")
                    }

                    section("Model")
                    row("Drafting") {
                        value(settings.model.enabled ? "\(settings.model.name) · \(settings.model.provider)" : "Templates only")
                    }
                    hint(settings.model.enabled
                         ? "Change with: recap models set --provider ollama|openai --model <name>"
                         : "Local: recap models set --provider ollama --model llama3.1:8b")
                }
            }
            .scrollIndicators(.never)
            footer.padding(.top, 12)
        }
        .padding(EdgeInsets(top: 18, leading: 20, bottom: 16, trailing: 20))
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .glassPanel(radius: 24, elevated: true)
        .padding(ShadowInset.size)
    }

    private var canInstallCLI: Bool { !settings.cliInstalled && CLIInstaller.canInstall }

    private var header: some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text("Settings").font(.system(size: 20, weight: .semibold))
            Spacer()
            Label("Recap \(appVersion)", systemImage: "arrow.counterclockwise")
                .labelStyle(.titleAndIcon)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
        }
    }

    private var footer: some View {
        HStack(spacing: 14) {
            HStack(spacing: 6) { KeyLabel(keys: hotkey.current.display); Text("quick context") }
            HStack(spacing: 6) { KeyLabel(keys: "esc"); Text("close") }
            Spacer()
            Button("Quit Recap") { NSApp.terminate(nil) }.buttonStyle(.borderless)
        }
        .font(.system(size: 13))
        .foregroundStyle(.secondary)
    }

    private func section(_ title: String, first: Bool = false) -> some View {
        Text(title)
            .font(.system(size: 12, weight: .medium))
            .foregroundStyle(.secondary)
            .padding(.horizontal, 12)
            .padding(.top, first ? 0 : 14)
            .padding(.bottom, 4)
    }

    private func row<V: View>(_ label: String, selected: Bool = false, action: (() -> Void)? = nil,
                              @ViewBuilder trailing: () -> V) -> some View {
        PreferenceRow(label: label, selected: selected, action: action, trailing: trailing())
    }

    private func value(_ text: String, accent: Bool = false) -> some View {
        Text(text)
            .font(.system(size: 14))
            .lineLimit(1)
            .truncationMode(.middle)
            .foregroundStyle(accent ? AnyShapeStyle(.tint) : AnyShapeStyle(.secondary))
    }

    private func hint(_ text: String) -> some View {
        Text(text)
            .font(.system(size: 12))
            .foregroundStyle(.tertiary)
            .fixedSize(horizontal: false, vertical: true)
            .padding(.horizontal, 12)
            .padding(.top, 2)
    }

    private func publishLabel(_ p: String?) -> String {
        switch p {
        case "pr": "Pull requests"
        case "push": "Push"
        default: "Local only"
        }
    }

    private func open(_ repo: String?) {
        guard let repo else { return }
        NSWorkspace.shared.open(URL(fileURLWithPath: (repo as NSString).expandingTildeInPath))
    }

    private var appVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "dev"
    }
}

/// Label on the left, value or control on the right; the whole row is clickable.
struct PreferenceRow<Trailing: View>: View {
    let label: String
    var selected = false
    var action: (() -> Void)?
    let trailing: Trailing
    @State private var hover = false

    var body: some View {
        HStack(spacing: 10) {
            if selected {
                Image(systemName: "checkmark").font(.system(size: 12, weight: .semibold)).foregroundStyle(.tint)
            }
            Text(label).font(.system(size: 14)).lineLimit(1)
            Spacer(minLength: 12)
            trailing
        }
        .padding(.horizontal, 12)
        .frame(minHeight: 34)
        .background(RoundedRectangle(cornerRadius: 10, style: .continuous)
            .fill(hover && action != nil ? Color.primary.opacity(0.06) : .clear))
        .contentShape(Rectangle())
        .onHover { hover = $0 }
        .onTapGesture { action?() }
    }
}
