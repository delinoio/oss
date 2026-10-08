# Terminal dependency notices

The DeliDev terminal bundles xterm.js 6.0.0, its WebGL addon 0.19.0 and Fit
addon 0.11.0 from [the xterm.js project](https://github.com/xtermjs/xterm.js).
Their original MIT license texts and copyright notices are included beside
this notice. These dependencies retain their original licenses.

DeliDev modifies xterm's Viewport stylesheet creation and renderer fallback to
support its Content Security Policy, and directs both package entrypoints to
the same modified ESM build. The addons remain unchanged.
