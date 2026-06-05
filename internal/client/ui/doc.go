// Package ui is the Fyne desktop shell for the lanweave client: the first-run wizard and
// the placeholder home area. All Fyne-dependent code is built with the "gui" build tag and
// the desktop/GL toolchain; this untagged file keeps the package valid on headless hosts
// (where only the Fyne-free client core is compiled and tested).
package ui
