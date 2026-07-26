import Foundation
import SwiftRs
import Tauri
import UIKit
import UniformTypeIdentifiers

private final class ExportDiagnosticsArgs: Decodable {
    let fileName: String
    let bundle: String
}

private struct ExportDiagnosticsResponse: Encodable {
    let status: String
}

private enum DiagnosticsExportErrorCode: String {
    case busy
    case pickerUnavailable = "picker-unavailable"
    case writeFailed = "write-failed"
}

final class DevHudDiagnosticsPlugin: Plugin, UIDocumentPickerDelegate {
    private var pendingInvoke: Invoke?
    private var temporaryURL: URL?

    @objc func exportDiagnostics(_ invoke: Invoke) {
        guard pendingInvoke == nil else {
            reject(invoke, .busy)
            return
        }
        if let temporaryURL {
            do {
                try FileManager.default.removeItem(at: temporaryURL)
                self.temporaryURL = nil
            } catch {
                reject(invoke, .writeFailed)
                return
            }
        }
        let arguments: ExportDiagnosticsArgs
        do {
            arguments = try invoke.parseArgs(ExportDiagnosticsArgs.self)
        } catch {
            reject(invoke, .pickerUnavailable)
            return
        }
        let safeName = URL(fileURLWithPath: arguments.fileName).lastPathComponent
        let destination = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString, isDirectory: true)
        let source = destination.appendingPathComponent(safeName)
        do {
            try FileManager.default.createDirectory(
                at: destination,
                withIntermediateDirectories: false
            )
            temporaryURL = destination
            try arguments.bundle.write(
                to: source,
                atomically: true,
                encoding: .utf8
            )
        } catch {
            if temporaryURL != nil {
                removeStagingAfterFailure(destination)
            }
            reject(invoke, .writeFailed)
            return
        }
        guard let viewController = manager.viewController else {
            removeStagingAfterFailure(destination)
            reject(invoke, .pickerUnavailable)
            return
        }
        pendingInvoke = invoke
        DispatchQueue.main.async {
            let picker = UIDocumentPickerViewController(
                forExporting: [source],
                asCopy: true
            )
            picker.delegate = self
            picker.modalPresentationStyle = .fullScreen
            viewController.present(
                picker,
                animated: true
            )
        }
    }

    func documentPickerWasCancelled(_ controller: UIDocumentPickerViewController) {
        finish(status: "cancelled")
    }

    func documentPicker(
        _ controller: UIDocumentPickerViewController,
        didPickDocumentsAt urls: [URL]
    ) {
        finish(status: urls.isEmpty ? nil : "exported")
    }

    private func finish(status: String?) {
        let invoke = pendingInvoke
        pendingInvoke = nil
        if let temporaryURL {
            do {
                try FileManager.default.removeItem(at: temporaryURL)
            } catch {
                if let invoke {
                    reject(invoke, .writeFailed)
                }
                return
            }
        }
        temporaryURL = nil
        guard let invoke else {
            return
        }
        guard let status else {
            reject(invoke, .writeFailed)
            return
        }
        invoke.resolve(ExportDiagnosticsResponse(status: status))
    }

    private func removeStagingAfterFailure(_ url: URL) {
        do {
            try FileManager.default.removeItem(at: url)
            temporaryURL = nil
        } catch {
            temporaryURL = url
        }
    }

    private func reject(
        _ invoke: Invoke,
        _ code: DiagnosticsExportErrorCode
    ) {
        invoke.reject(
            "The DevHud diagnostics export failed.",
            code: code.rawValue
        )
    }
}

@_cdecl("init_plugin_devhud_diagnostics")
func initPlugin() -> Plugin {
    DevHudDiagnosticsPlugin()
}
