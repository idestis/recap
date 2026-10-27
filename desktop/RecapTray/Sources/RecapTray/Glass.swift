import SwiftUI

/// Recap's glass surface (SPEC D-21). macOS 26 gets system Liquid Glass; earlier
/// versions get a material fill with a hairline highlight.
struct GlassSurface: ViewModifier {
    var cornerRadius: CGFloat
    var elevated = false

    func body(content: Content) -> some View {
        let shape = RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
        Group {
            if #available(macOS 26.0, *) {
                content.glassEffect(.regular, in: shape)
            } else {
                content
                    .background(.regularMaterial, in: shape)
                    .overlay { shape.strokeBorder(.white.opacity(0.18), lineWidth: 1) }
            }
        }
        .compositingGroup()
        .shadow(color: .black.opacity(elevated ? 0.16 : 0.10), radius: elevated ? 18 : 10, y: elevated ? 9 : 4)
    }
}

extension View {
    func glassPanel(radius: CGFloat, elevated: Bool = false) -> some View {
        modifier(GlassSurface(cornerRadius: radius, elevated: elevated))
    }
}

/// Floating windows are larger than their glass by this inset on every side,
/// so the SwiftUI shadow fades out instead of being cut at the window edge.
/// The window's own shadow stays off.
enum ShadowInset { static let size: CGFloat = 48 }

/// A shortcut rendered like a physical key, e.g. ⌥⇧Space.
struct KeyLabel: View {
    let keys: String

    var body: some View {
        Text(keys)
            .font(.system(size: 12, weight: .medium, design: .rounded))
            .monospacedDigit()
            .padding(EdgeInsets(top: 2, leading: 6, bottom: 2, trailing: 6))
            .background {
                RoundedRectangle(cornerRadius: 5, style: .continuous)
                    .fill(.primary.opacity(0.06))
                    .overlay { RoundedRectangle(cornerRadius: 5, style: .continuous).strokeBorder(.primary.opacity(0.14)) }
            }
            .fixedSize()
    }
}
