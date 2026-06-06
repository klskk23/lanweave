# Feature Specification: Windows App Icon

**Feature Branch**: `016-windows-app-icon`

**Created**: 2026-06-06

**Status**: Draft

**Input**: User description: "为 windows 程序添加图标，图标位于 packaging/icon.svg ，检查过往 spec 与代码，选择合适的插入点"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - The installed program shows the lanweave icon (Priority: P1)

A user installs the lanweave desktop client and then finds it in the usual places —
the installed program file in its folder, the Start-menu shortcut, the desktop
shortcut, the taskbar while it runs, and the Alt+Tab switcher. In every one of these
places the program is shown with the lanweave brand icon, not a blank or generic
default icon.

**Why this priority**: This is the core of the feature and the part the user sees most
often. A program whose file and shortcuts carry no icon looks broken or untrustworthy;
giving the executable its own embedded icon is what makes every place that displays the
program inherit the brand mark. Without this nothing else matters.

**Independent Test**: Install the client on a clean Windows machine, then look at the
program file in its install folder, the Start-menu entry, the desktop shortcut, and
(after launching it) the taskbar and Alt+Tab. Each shows the lanweave icon. Delivers
recognizable branding for the program itself.

**Acceptance Scenarios**:

1. **Given** the client is installed, **When** the user views the program file in its
   install folder, **Then** the file is displayed with the lanweave icon (not the
   default application icon).
2. **Given** the client is installed, **When** the user opens the Start menu and the
   desktop, **Then** both shortcuts to the client show the lanweave icon.
3. **Given** the client is running, **When** the user looks at the taskbar and the
   Alt+Tab switcher, **Then** the running program is represented by the lanweave icon.

---

### User Story 2 - The running window shows the lanweave icon (Priority: P1)

While the desktop client is open, its window carries the lanweave icon in the window's
title bar (and wherever the operating system shows a window's own icon). The user can
visually tie the open window to the lanweave brand at a glance.

**Why this priority**: The executable's embedded icon covers the file and taskbar, but
the toolkit that draws the application window supplies the in-window icon separately. If
this is not set, the window itself falls back to the toolkit's default mark even though
the file has the right icon — an inconsistent, unbranded look during the user's actual
working session. It is the same brand mark, applied at the window layer.

**Independent Test**: Launch the client and observe the window's title-bar icon (and, on
platforms that show it, the per-window taskbar grouping icon). It is the lanweave icon.
This can be verified independently of the installer.

**Acceptance Scenarios**:

1. **Given** the client is launched, **When** the main window appears, **Then** the
   window's own icon is the lanweave icon.
2. **Given** the client window is open, **When** the user inspects the running app's
   in-window branding, **Then** it is visually consistent with the installed program's
   icon (same brand mark).

---

### User Story 3 - The installer, uninstaller, and program list show the lanweave icon (Priority: P2)

When the user runs the setup program, the setup window and its file carry the lanweave
icon. After installation, the entry in the operating system's "installed programs"
(Add/Remove Programs) list shows the lanweave icon together with a recognizable program
name, version, and publisher. When the user later uninstalls, the uninstaller carries
the lanweave icon too.

**Why this priority**: These touch points appear less often than the program itself (only
at install/uninstall and when browsing installed programs), so they rank below P1, but a
branded installer and a complete, branded entry in the programs list make the software
look finished and legitimate rather than anonymous.

**Independent Test**: Run the setup program (its file and window show the icon), open the
operating system's installed-programs list (the lanweave entry shows the icon plus a
version and publisher), then start the uninstaller (it shows the icon). Each is
observable without the others.

**Acceptance Scenarios**:

1. **Given** the setup program file, **When** the user views or launches it, **Then** the
   setup file and window display the lanweave icon.
2. **Given** the client has been installed, **When** the user opens the installed-programs
   list, **Then** the lanweave entry shows the lanweave icon, a program version, and a
   publisher name.
3. **Given** the client is installed, **When** the user starts the uninstaller, **Then**
   the uninstaller displays the lanweave icon.

---

### Edge Cases

- **Headless / non-graphical build of the client**: The project also builds a
  non-graphical placeholder of the client (used on build/test hosts that have no desktop
  toolkit). Adding the icon MUST NOT break that build — the icon belongs only to the real
  desktop program, and the placeholder must keep compiling and running unchanged.
- **Rebuilding the icon from source**: The brand source is a single vector file
  (`packaging/icon.svg`). The various raster forms the platforms need MUST be
  reproducible from that one source by a documented, repeatable step, so the icon can be
  changed later by editing the vector and regenerating — not by hand-editing binaries.
- **Forgetting to generate the icon before building**: The release pipeline regenerates the
  embedded-icon resource from source before every build, so released programs are never
  shipped without it. A local developer who compiles without first running the documented
  `make icons` step gets an unbranded local build — a known, documented dev-only caveat, not
  a shipped defect.
- **Display at small and large sizes**: The program is shown at many sizes (small list
  icons through large high-DPI tiles). The icon MUST remain legible across the common
  sizes rather than appearing blurred because only one size was supplied.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The Windows client program file MUST embed the lanweave icon so that the
  file itself, and everything that inherits an executable's icon (Start-menu and desktop
  shortcuts, taskbar, Alt+Tab), display the lanweave brand mark.
- **FR-002**: The running desktop client window MUST display the lanweave icon as its own
  window icon, visually consistent with the program file's icon.
- **FR-003**: The setup (installer) program MUST display the lanweave icon as its own
  program/window icon.
- **FR-004**: The uninstaller MUST display the lanweave icon as its own program/window
  icon.
- **FR-005**: The operating system's installed-programs (Add/Remove Programs) entry for
  the client MUST show the lanweave icon and MUST include a program version and a
  publisher name.
- **FR-006**: All raster icon forms required by the above MUST be generated from the
  single vector source `packaging/icon.svg` by a documented, repeatable procedure; the
  procedure MUST NOT require hand-editing of binary image files.
- **FR-007**: The icon MUST be supplied in the set of sizes the platform commonly
  requests (small through large/high-DPI) so it stays legible rather than being scaled
  from a single size.
- **FR-008**: The release/build process MUST ensure shipped programs contain the
  embedded-icon resource: the release pipeline regenerates it from `packaging/icon.svg`
  before building, so a released program never silently loses its icon. Local developer
  builds produce the resource via the documented `make icons` step (see quickstart) rather
  than by automatic regeneration inside a bare compile.
- **FR-009**: Adding the icon MUST NOT break the non-graphical placeholder build of the
  client used on hosts without a desktop toolkit.
- **FR-010**: The project's automated build/release process MUST produce installer and
  program artifacts that carry the icon, so released downloads are branded without a
  manual step.

### Key Entities *(include if feature involves data)*

- **Icon source**: The single committed vector artwork (`packaging/icon.svg`) that is the
  authoritative brand mark; every raster form derives from it.
- **Multi-size raster icon**: The platform icon container holding several pixel sizes,
  consumed by the program file's embedded resource and by the installer/uninstaller.
- **Window icon image**: A single raster image embedded into the desktop program and used
  by the graphical toolkit as the running window's icon.
- **Installed-program entry**: The operating system's record of the installed client,
  whose displayed icon, version, and publisher are set at install time.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On a clean Windows install, 100% of the five user-visible surfaces — program
  file, Start-menu/desktop shortcuts, running taskbar/Alt+Tab, running window title bar,
  and the installed-programs entry — display the lanweave icon (no blank or generic
  default remains).
- **SC-002**: The setup program and the uninstaller each display the lanweave icon.
- **SC-003**: The installed-programs entry shows the lanweave icon together with a
  non-empty version and a non-empty publisher.
- **SC-004**: Editing `packaging/icon.svg` and running the documented regeneration step
  reproduces every raster icon form with no manual binary editing.
- **SC-005**: The non-graphical placeholder build of the client continues to build and run
  unchanged after the icon is added.
- **SC-006**: An automated release of the client yields installer and program artifacts
  that already carry the icon, with no manual post-build icon step.

## Assumptions

- The brand artwork already exists and is final for this feature as `packaging/icon.svg`
  (a square vector image); designing or revising the artwork is out of scope.
- "The Windows client" refers to the existing desktop client program and the installer
  already produced by the project; this feature adds icons to those, it does not introduce
  a new program or a new installer technology.
- The publisher name shown in the installed-programs list is the project/brand name
  ("lanweave"); no separate legal entity name is required for v1.
- The version shown in the installed-programs list is the same version string the release
  process already assigns to the build; this feature reuses it rather than defining a new
  versioning scheme.
- The graphical toolkit applies a single supplied window image and scales it as needed; a
  separate per-size set for the in-window icon is not required (the multi-size set is only
  needed for the executable/installer icon container).
- Visual confirmation of the icon on the five surfaces is performed manually on Windows;
  there is no automated pixel inspection of the rendered icon, consistent with the
  project's existing manual exception for Windows GUI verification.
