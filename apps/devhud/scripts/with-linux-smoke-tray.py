#!/usr/bin/python3
"""Run headless Linux smoke commands with a session-local tray host fixture."""

import signal
import subprocess
import sys

import dbus
import dbus.service
from dbus.mainloop.glib import DBusGMainLoop
from gi.repository import GLib


INTERFACE = "org.kde.StatusNotifierWatcher"


class SmokeTrayWatcher(dbus.service.Object):
    """Model tray registration; this fixture does not render desktop-panel icons."""

    def __init__(self, bus):
        self.items = []
        self.name = dbus.service.BusName(INTERFACE, bus, do_not_queue=True)
        super().__init__(bus, "/StatusNotifierWatcher")

    @dbus.service.method(INTERFACE, in_signature="s", out_signature="", sender_keyword="sender")
    def RegisterStatusNotifierItem(self, service, sender=None):
        item = sender + service if service.startswith("/") else service
        if item not in self.items:
            self.items.append(item)
            self.StatusNotifierItemRegistered(item)

    @dbus.service.method(INTERFACE, in_signature="s", out_signature="")
    def RegisterStatusNotifierHost(self, service):
        pass

    @dbus.service.signal(INTERFACE, signature="s")
    def StatusNotifierItemRegistered(self, service):
        pass

    @dbus.service.method("org.freedesktop.DBus.Properties", in_signature="s", out_signature="a{sv}")
    def GetAll(self, interface):
        if interface != INTERFACE:
            raise dbus.exceptions.DBusException("unknown fixture interface")
        return {
            "RegisteredStatusNotifierItems": dbus.Array(self.items, signature="s"),
            "IsStatusNotifierHostRegistered": dbus.Boolean(True),
            "ProtocolVersion": dbus.Int32(0),
        }

    @dbus.service.method("org.freedesktop.DBus.Properties", in_signature="ss", out_signature="v")
    def Get(self, interface, name):
        return self.GetAll(interface)[name]


def main():
    if len(sys.argv) < 2:
        raise SystemExit("Usage: with-linux-smoke-tray.py <command> [arguments...]")
    DBusGMainLoop(set_as_default=True)
    bus = dbus.SessionBus()
    watcher = SmokeTrayWatcher(bus)
    loop = GLib.MainLoop()
    child = subprocess.Popen(sys.argv[1:])

    def forward_signal(number, _frame):
        if child.poll() is None:
            child.send_signal(number)

    def poll_child():
        if child.poll() is None:
            return True
        loop.quit()
        return False

    signal.signal(signal.SIGINT, forward_signal)
    signal.signal(signal.SIGTERM, forward_signal)
    GLib.timeout_add(100, poll_child)
    try:
        loop.run()
    finally:
        watcher.remove_from_connection()
        bus.close()
        if child.poll() is None:
            child.terminate()
        result = child.wait()
    return result if result >= 0 else 128 - result


if __name__ == "__main__":
    raise SystemExit(main())
