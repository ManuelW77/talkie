#include "my_application.h"

int main(int argc, char** argv) {
  // X11: WM_CLASS = de.talkie.talkie, damit die .desktop-Datei (StartupWMClass) passt
  // und Dock/Taskleiste das Talkie-Icon zeigen. Wayland nutzt ohnehin die Application-ID.
  g_set_prgname("de.talkie.talkie");
  g_autoptr(MyApplication) app = my_application_new();
  return g_application_run(G_APPLICATION(app), argc, argv);
}
