// Rendert ein SVG als PNG: swift tool/render_svg.swift in.svg out.png 1024 [hoehe]
import AppKit
let a = CommandLine.arguments
guard a.count == 4 || a.count == 5, let img = NSImage(contentsOfFile: a[1]), let n = Int(a[3]) else { fatalError("usage: in.svg out.png breite [hoehe]")}
let h = a.count == 5 ? Int(a[4]) ?? n : n
let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: n, pixelsHigh: h, bitsPerSample: 8, samplesPerPixel: 4,
  hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
img.draw(in: NSRect(x: 0, y: 0, width: n, height: h))
NSGraphicsContext.restoreGraphicsState()
try! rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: a[2]))
