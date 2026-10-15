// SPDX-License-Identifier: Apache-2.0
// A no-entrypoint PE DLL with no CRT/import dependencies. The native fixture
// loads it through a ZIP-backed logical package pathname.
__declspec(dllexport) int pnport_fixture_value(void) { return 42; }
