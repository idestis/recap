import Foundation

// MARK: - Models (decoded defensively: every field optional, never crash)

struct RecapStatus: Decodable, Sendable {
    struct Session: Decodable, Sendable {
        var handle: String
        var name: String
        var description: String
        var started_at: String?
        var links: [String: String]?
        var tabs: [String]?
        var space: String?

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            handle = (try? c.decode(String.self, forKey: .handle)) ?? ""
            name = (try? c.decode(String.self, forKey: .name)) ?? ""
            description = (try? c.decode(String.self, forKey: .description)) ?? ""
            started_at = try? c.decode(String.self, forKey: .started_at)
            links = try? c.decode([String: String].self, forKey: .links)
            tabs = try? c.decode([String].self, forKey: .tabs)
            space = try? c.decode(String.self, forKey: .space)
        }
        enum CodingKeys: String, CodingKey { case handle, name, description, started_at, links, tabs, space }
    }

    var active: Bool
    var session: Session?
    var counts: [String: Int]?
    var elapsed: String?
    var space: String?

    init(active: Bool, session: Session?, counts: [String: Int]?, elapsed: String?, space: String?) {
        self.active = active; self.session = session; self.counts = counts
        self.elapsed = elapsed; self.space = space
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        active = (try? c.decode(Bool.self, forKey: .active)) ?? false
        session = try? c.decode(Session.self, forKey: .session)
        counts = try? c.decode([String: Int].self, forKey: .counts)
        elapsed = try? c.decode(String.self, forKey: .elapsed)
        space = try? c.decode(String.self, forKey: .space)
    }
    enum CodingKeys: String, CodingKey { case active, session, counts, elapsed, space }

    static let idle = RecapStatus(active: false, session: nil, counts: nil, elapsed: nil, space: nil)
}

struct RecapSpace: Decodable, Sendable, Hashable {
    var name: String
    var repo: String?
    var remote: String?
    var publish: String?
    var paths: [String]?

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decode(String.self, forKey: .name)
        repo = try? c.decode(String.self, forKey: .repo)
        remote = try? c.decode(String.self, forKey: .remote)
        publish = try? c.decode(String.self, forKey: .publish)
        paths = try? c.decode([String].self, forKey: .paths)
    }
    enum CodingKeys: String, CodingKey { case name, repo, remote, publish, paths }
}

struct RecapSpaces: Decodable, Sendable {
    var current: String?
    var spaces: [RecapSpace]

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        current = try? c.decode(String.self, forKey: .current)
        // Skip malformed entries instead of failing the whole list.
        var list: [RecapSpace] = []
        if var arr = try? c.nestedUnkeyedContainer(forKey: .spaces) {
            while !arr.isAtEnd {
                if let s = try? arr.decode(RecapSpace.self) { list.append(s) } else { _ = try? arr.decode(Skip.self) }
            }
        }
        spaces = list
    }
    enum CodingKeys: String, CodingKey { case current, spaces }
    private struct Skip: Decodable {}

    var currentSpace: RecapSpace? { spaces.first { $0.name == current } }
}

struct CLIResult: Sendable {
    var code: Int32
    var out: String
    var err: String
    var ok: Bool { code == 0 }
}

// MARK: - Bridge to the recap CLI

enum RecapCLI {
    static let bundledPath = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/recap").path

    /// Bundled CLI, then RECAP_BIN, then common install locations, then PATH.
    static var binary: String {
        let fm = FileManager.default
        if fm.isExecutableFile(atPath: bundledPath) { return bundledPath }
        if let env = ProcessInfo.processInfo.environment["RECAP_BIN"], !env.isEmpty { return env }
        let home = NSHomeDirectory()
        for path in ["/opt/homebrew/bin/recap", "/usr/local/bin/recap", home + "/go/bin/recap", home + "/.local/bin/recap"]
        where fm.isExecutableFile(atPath: path) {
            return path
        }
        return "/usr/bin/env"
    }

    private final class Box: @unchecked Sendable { var data = Data() }

    /// Blocking. Call from a background task.
    @discardableResult
    static func run(_ args: [String], stdin: String? = nil) -> CLIResult {
        let process = Process()
        let bin = binary
        process.executableURL = URL(fileURLWithPath: bin)
        process.arguments = bin == "/usr/bin/env" ? ["recap"] + args : args
        let out = Pipe(), err = Pipe(), input = Pipe()
        process.standardOutput = out
        process.standardError = err
        process.standardInput = input
        process.environment = ProcessInfo.processInfo.environment.merging(["NO_COLOR": "1"]) { $1 }
        do { try process.run() } catch { return CLIResult(code: -1, out: "", err: error.localizedDescription) }

        let errBox = Box()
        let group = DispatchGroup()
        group.enter()
        DispatchQueue.global().async {
            errBox.data = err.fileHandleForReading.readDataToEndOfFile()
            group.leave()
        }
        if let stdin { try? input.fileHandleForWriting.write(contentsOf: Data(stdin.utf8)) }
        try? input.fileHandleForWriting.close()
        let o = out.fileHandleForReading.readDataToEndOfFile()
        group.wait()
        process.waitUntilExit()
        return CLIResult(code: process.terminationStatus,
                         out: String(decoding: o, as: UTF8.self),
                         err: String(decoding: errBox.data, as: UTF8.self))
    }

    static func runAsync(_ args: [String], stdin: String? = nil) async -> CLIResult {
        await Task.detached { run(args, stdin: stdin) }.value
    }
}

@MainActor
final class StatusModel: ObservableObject {
    static let shared = StatusModel()
    @Published var status = RecapStatus.idle
    @Published var spaces: RecapSpaces?
    private var timer: Timer?
    private var ticks = 0

    var currentSpaceName: String? { status.space ?? spaces?.current }

    func startPolling() {
        refresh()
        refreshSpaces()
        timer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { _ in
            Task { @MainActor in
                let m = StatusModel.shared
                m.ticks += 1
                m.refresh()
                if m.ticks % 15 == 0 { m.refreshSpaces() }
            }
        }
    }

    func refresh() {
        Task {
            let r = await RecapCLI.runAsync(["status", "--json"])
            status = (try? JSONDecoder().decode(RecapStatus.self, from: Data(r.out.utf8))) ?? .idle
        }
    }

    func refreshSpaces() {
        Task {
            let r = await RecapCLI.runAsync(["spaces", "--json"])
            if r.ok, let s = try? JSONDecoder().decode(RecapSpaces.self, from: Data(r.out.utf8)), !s.spaces.isEmpty {
                spaces = s
            } else {
                spaces = nil // hide the UI
            }
        }
    }

    func useSpace(_ name: String) {
        Task {
            _ = await RecapCLI.runAsync(["spaces", "use", name])
            refreshSpaces()
            refresh()
        }
    }
}
