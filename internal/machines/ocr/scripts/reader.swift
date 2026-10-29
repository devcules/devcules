// reader.swift: reads words and their boxes from an image with Vision
// (macOS's own OCR). Argument 1: the PNG path. Output, one word per
// line, is the shared TSV every OCR engine prints:
//
//   text\tx\ty\tw\th
//
// Boxes are pixels, top-left origin, matching the frame.
import Foundation
import Vision
import AppKit

guard CommandLine.arguments.count > 1,
      let img = NSImage(contentsOfFile: CommandLine.arguments[1]),
      let cg = img.cgImage(forProposedRect: nil, context: nil, hints: nil) else {
  FileHandle.standardError.write("reader: cannot load image".data(using: .utf8)!)
  exit(1)
}

let request = VNRecognizeTextRequest()
request.recognitionLevel = .accurate
let handler = VNImageRequestHandler(cgImage: cg, options: [:])
try handler.perform([request])
for obs in request.results ?? [] {
  guard let cand = obs.topCandidates(1).first else { continue }
  let text = cand.string.replacingOccurrences(of: "\t", with: " ").replacingOccurrences(of: "\n", with: " ")
  // boundingBox(for:) answers the box of a string range, normalised
  // bottom-left origin; convert to pixels, top-left origin.
  guard let box = try? cand.boundingBox(for: text.startIndex..<text.endIndex) else { continue }
  let r = box.boundingBox
  let x = Int(r.origin.x * CGFloat(cg.width))
  let y = Int((1 - r.origin.y - r.height) * CGFloat(cg.height))
  let w = Int(r.width * CGFloat(cg.width))
  let h = Int(r.height * CGFloat(cg.height))
  print("\(text)\t\(x)\t\(y)\t\(w)\t\(h)")
}
