import AppKit
import Carbon.HIToolbox
import SwiftUI

/// A global shortcut: Carbon key code and modifier mask, plus the key's label as
/// it was typed when recorded (so non-US layouts show the right character).
struct Shortcut: Codable, Equatable, Hashable, Sendable {
    var keyCode: UInt32
    var modifiers: UInt32
    var key: String?

    // Claude Desktop owns ⌥Space, so the default is ⌥⇧Space.
    static let `default` = Shortcut(keyCode: UInt32(kVK_Space), modifiers: UInt32(optionKey | shiftKey), key: "Space")

    /// Requires ⌘, ⌥ or ⌃ so ordinary typing can never fire it.
    var isValid: Bool { modifiers & UInt32(cmdKey | optionKey | controlKey) != 0 }

    var keyLabel: String { Self.named(keyCode) ?? key ?? "Key \(keyCode)" }

    var display: String {
        let glyphs: [(Int, String)] = [(controlKey, "⌃"), (optionKey, "⌥"), (shiftKey, "⇧"), (cmdKey, "⌘")]
        return glyphs.filter { modifiers & UInt32($0.0) != 0 }.map(\.1).joined() + keyLabel
    }

    /// Builds a shortcut from a key press while recording.
    init(event e: NSEvent) {
        keyCode = UInt32(e.keyCode)
        var m = 0
        if e.modifierFlags.contains(.control) { m |= controlKey }
        if e.modifierFlags.contains(.option) { m |= optionKey }
        if e.modifierFlags.contains(.shift) { m |= shiftKey }
        if e.modifierFlags.contains(.command) { m |= cmdKey }
        modifiers = UInt32(m)
        key = Self.named(keyCode) ?? e.charactersIgnoringModifiers.map { $0.uppercased() }
    }

    init(keyCode: UInt32, modifiers: UInt32, key: String? = nil) {
        self.keyCode = keyCode
        self.modifiers = modifiers
        self.key = key
    }

    /// Keys whose label doesn't depend on the keyboard layout.
    private static func named(_ code: UInt32) -> String? {
        switch Int(code) {
        case kVK_Space: "Space"
        case kVK_Return: "↩"
        case kVK_Tab: "⇥"
        case kVK_Delete: "⌫"
        case kVK_ForwardDelete: "⌦"
        case kVK_Escape: "⎋"
        case kVK_LeftArrow: "←"
        case kVK_RightArrow: "→"
        case kVK_UpArrow: "↑"
        case kVK_DownArrow: "↓"
        case kVK_F1...kVK_F20 where fKeys[Int(code)] != nil: fKeys[Int(code)]
        default: nil
        }
    }

    private static let fKeys: [Int: String] = [
        kVK_F1: "F1", kVK_F2: "F2", kVK_F3: "F3", kVK_F4: "F4", kVK_F5: "F5", kVK_F6: "F6",
        kVK_F7: "F7", kVK_F8: "F8", kVK_F9: "F9", kVK_F10: "F10", kVK_F11: "F11", kVK_F12: "F12",
    ]

    /// The same keys as a menu key equivalent, so the native menu shows them.
    /// Display only: the Carbon hotkey does the real work.
    var menuShortcut: KeyboardShortcut? {
        let k: KeyEquivalent
        switch Int(keyCode) {
        case kVK_Space: k = .space
        case kVK_Return: k = .return
        case kVK_Tab: k = .tab
        default:
            guard let label = key, label.count == 1, let ch = label.lowercased().first else { return nil }
            k = KeyEquivalent(ch)
        }
        var mods: SwiftUI.EventModifiers = []
        if modifiers & UInt32(controlKey) != 0 { mods.insert(.control) }
        if modifiers & UInt32(optionKey) != 0 { mods.insert(.option) }
        if modifiers & UInt32(shiftKey) != 0 { mods.insert(.shift) }
        if modifiers & UInt32(cmdKey) != 0 { mods.insert(.command) }
        return KeyboardShortcut(k, modifiers: mods)
    }
}

/// Owns the overlay shortcut: persisted as JSON in UserDefaults, registered with Carbon.
@MainActor
final class HotKeyManager: ObservableObject {
    static let shared = HotKeyManager()
    private static let storeKey = "hotkey.shortcut"
    @Published private(set) var current: Shortcut
    @Published private(set) var registrationFailed = false
    private var hotKey: HotKey?

    private init() {
        let d = UserDefaults.standard
        if let data = d.data(forKey: Self.storeKey), let s = try? JSONDecoder().decode(Shortcut.self, from: data) {
            current = s
        } else if d.object(forKey: "hotkey.keyCode") != nil { // first prototype stored two ints
            current = Shortcut(keyCode: UInt32(d.integer(forKey: "hotkey.keyCode")), modifiers: UInt32(d.integer(forKey: "hotkey.modifiers")))
        } else {
            current = .default
        }
    }

    func register() {
        hotKey = nil // unregisters the previous one
        let hk = HotKey(keyCode: current.keyCode, modifiers: current.modifiers) {
            Task { @MainActor in OverlayController.shared.toggle() }
        }
        registrationFailed = !hk.registered
        hotKey = hk
    }

    /// While recording, the old shortcut must not fire (and must be recordable).
    func pause() { hotKey = nil }

    func set(_ s: Shortcut) {
        current = s
        if let data = try? JSONEncoder().encode(s) { UserDefaults.standard.set(data, forKey: Self.storeKey) }
        register()
    }
}

/// Global hotkey via Carbon (no Accessibility or Input Monitoring permission).
final class HotKey {
    private var ref: EventHotKeyRef?
    nonisolated(unsafe) private static var handlers: [UInt32: () -> Void] = [:]
    nonisolated(unsafe) private static var installed = false
    nonisolated(unsafe) private static var nextID: UInt32 = 1
    private let id: UInt32
    let registered: Bool

    init(keyCode: UInt32, modifiers: UInt32, handler: @escaping () -> Void) {
        id = HotKey.nextID
        HotKey.nextID += 1
        HotKey.handlers[id] = handler
        HotKey.installHandlerIfNeeded()
        let hotKeyID = EventHotKeyID(signature: OSType(0x5243_4150), id: id) // "RCAP"
        registered = RegisterEventHotKey(keyCode, modifiers, hotKeyID, GetApplicationEventTarget(), 0, &ref) == noErr
    }

    deinit {
        if let ref { UnregisterEventHotKey(ref) }
        HotKey.handlers[id] = nil
    }

    private static func installHandlerIfNeeded() {
        guard !installed else { return }
        installed = true
        var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
        InstallEventHandler(GetApplicationEventTarget(), { _, event, _ in
            var hk = EventHotKeyID()
            GetEventParameter(event, EventParamName(kEventParamDirectObject), EventParamType(typeEventHotKeyID),
                              nil, MemoryLayout<EventHotKeyID>.size, nil, &hk)
            let id = hk.id
            DispatchQueue.main.async { HotKey.handlers[id]?() }
            return noErr
        }, 1, &spec, nil, nil)
    }
}
