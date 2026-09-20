//! Real native process creation that deliberately bypasses CreateProcess hooks.
use std::{mem, ptr};

use ntapi::{ntpsapi::*, ntrtl::*};
use winapi::shared::ntdef::UNICODE_STRING;
use windows_sys::Win32::{
    Foundation::CloseHandle,
    System::Threading::{GetExitCodeProcess, TerminateProcess, WaitForSingleObject},
};

fn unicode(text: &mut [u16]) -> UNICODE_STRING {
    UNICODE_STRING {
        Length: ((text.len() - 1) * 2).try_into().unwrap(),
        MaximumLength: (text.len() * 2).try_into().unwrap(),
        Buffer: text.as_mut_ptr(),
    }
}
fn wide(text: &str) -> Vec<u16> {
    text.encode_utf16().chain(Some(0)).collect()
}

pub fn spawn(output: &str) {
    let path = std::env::current_exe()
        .unwrap()
        .to_str()
        .unwrap()
        .to_owned();
    let path = path.strip_prefix(r"\\?\").unwrap_or(&path);
    let mut image = wide(&format!(r"\??\{path}"));
    let mut command = wide(&format!("\"{path}\" windows-native-leaf \"{output}\""));
    let mut image_string = unicode(&mut image);
    let mut command_string = unicode(&mut command);
    let mut parameters = ptr::null_mut();
    // SAFETY: all strings and NT structures remain live for the call. The
    // fixture owns and releases every successfully returned allocation/handle.
    unsafe {
        let status = RtlCreateProcessParametersEx(
            &mut parameters,
            &mut image_string,
            ptr::null_mut(),
            ptr::null_mut(),
            &mut command_string,
            ptr::null_mut(),
            ptr::null_mut(),
            ptr::null_mut(),
            ptr::null_mut(),
            ptr::null_mut(),
            RTL_USER_PROC_PARAMS_NORMALIZED,
        );
        assert!(status >= 0, "process parameters: {status:#x}");
        let mut info: PS_CREATE_INFO = mem::zeroed();
        info.Size = mem::size_of::<PS_CREATE_INFO>();
        info.State = PsCreateInitialState;
        let mut attributes: PS_ATTRIBUTE_LIST = mem::zeroed();
        attributes.TotalLength = mem::size_of::<PS_ATTRIBUTE_LIST>();
        attributes.Attributes[0].Attribute = PS_ATTRIBUTE_IMAGE_NAME;
        attributes.Attributes[0].Size = usize::from(image_string.Length);
        attributes.Attributes[0].u.ValuePtr = image_string.Buffer.cast();
        let mut process = ptr::null_mut();
        let mut thread = ptr::null_mut();
        let status = NtCreateUserProcess(
            &mut process,
            &mut thread,
            winapi::um::winnt::PROCESS_ALL_ACCESS,
            winapi::um::winnt::THREAD_ALL_ACCESS,
            ptr::null_mut(),
            ptr::null_mut(),
            0,
            0,
            parameters.cast(),
            &mut info,
            &mut attributes,
        );
        RtlDestroyProcessParameters(parameters);
        assert!(status >= 0, "native process creation: {status:#x}");
        // Creation success returns additional owned image/section handles.
        let created = info.u.SuccessState;
        for handle in [created.FileHandle, created.SectionHandle] {
            if !handle.is_null() {
                CloseHandle(handle.cast());
            }
        }
        let waited = WaitForSingleObject(process.cast(), 5000);
        if waited != 0 {
            TerminateProcess(process.cast(), 1);
            WaitForSingleObject(process.cast(), 5000);
        }
        let mut exit = u32::MAX;
        let read = GetExitCodeProcess(process.cast(), &mut exit);
        CloseHandle(thread.cast());
        CloseHandle(process.cast());
        assert_eq!(waited, 0, "native child wait");
        assert_ne!(read, 0);
        assert_eq!(exit, 0, "native child failed");
    }
}

/// Invalid user pointers must reach NT unchanged, without crashing
/// interception.
pub fn malformed_file_attributes() {
    use winapi::shared::ntdef::{OBJECT_ATTRIBUTES, POBJECT_ATTRIBUTES};
    let mut name = UNICODE_STRING {
        Length: 2,
        MaximumLength: 2,
        Buffer: 16usize as *mut u16,
    };
    // SAFETY: the kernel validates these deliberately invalid user pointers;
    // output storage and the local attribute headers remain live during calls.
    unsafe {
        let mut attributes: OBJECT_ATTRIBUTES = mem::zeroed();
        attributes.Length = mem::size_of::<OBJECT_ATTRIBUTES>() as u32;
        let mut information = mem::zeroed();
        for pointer in [ptr::null_mut(), 16usize as POBJECT_ATTRIBUTES] {
            let status = ntapi::ntioapi::NtQueryAttributesFile(pointer, &mut information);
            println!("{status}");
            assert!(status < 0);
        }
        attributes.ObjectName = 16usize as *mut UNICODE_STRING;
        let status = ntapi::ntioapi::NtQueryAttributesFile(&mut attributes, &mut information);
        println!("{status}");
        assert!(status < 0);
        attributes.ObjectName = &mut name;
        let status = ntapi::ntioapi::NtQueryAttributesFile(&mut attributes, &mut information);
        println!("{status}");
        assert!(status < 0);
    }
    println!("child-continued");
}

pub fn delete_file(path: &str) {
    use winapi::shared::ntdef::OBJECT_ATTRIBUTES;
    let path = path.strip_prefix(r"\\?\").unwrap_or(path);
    let mut path = wide(&format!(r"\??\{path}"));
    let mut name = unicode(&mut path);
    // SAFETY: the complete object name remains live for the native call.
    unsafe {
        let mut attributes: OBJECT_ATTRIBUTES = mem::zeroed();
        attributes.Length = mem::size_of::<OBJECT_ATTRIBUTES>() as u32;
        attributes.ObjectName = &mut name;
        let status = ntapi::ntioapi::NtDeleteFile(&mut attributes);
        println!("{status}");
    }
}
