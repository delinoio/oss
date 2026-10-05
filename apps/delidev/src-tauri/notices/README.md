# Native redistribution notices

Repository-owned DeliDev source and packages use Apache-2.0. Package resources
include the complete root `LICENSE` and `NOTICE` without changing their contents.

`CEF-LICENSE.txt` is copied without modification from Chromium Embedded Framework
commit [`8042e43`](https://github.com/chromiumembedded/cef/blob/8042e43/LICENSE.txt),
the CEF revision in the pinned 151.3.24 native distribution. It retains the
upstream BSD terms and original copyright notices.

The native dry-run scripts additionally copy the original `CREDITS.html` from
the exact platform's downloaded CEF 151.3.24 / Chromium 151.0.7922.174 distribution
into `notices/Chromium-CREDITS.html`. This generated file is not checked into the
repository. Verification compares the complete packaged files with their original
sources and records the Chromium credits digest. An engine/version change requires
reviewing these sources together; do not replace them with an Apache notice.
