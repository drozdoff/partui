package app

// selectedFileItem returns the selected item of the file list and reports
// whether an item is actually selected. It returns false when the list is
// empty, the item has an unexpected type, or the list is being filtered —
// a filter changes the meaning of the selected index, so acting on the
// selection is unsafe.
func selectedFileItem(m *Model) (fileListItem, bool) {
	it, ok := m.fileList.SelectedItem().(fileListItem)
	return it, ok && !m.fileList.SettingFilter()
}

// selectedIndexChanged returns the selected item of the index list and
// reports whether an item is actually selected, with the same guarantees
// as selectedFileItem.
func selectedIndexChanged(m *Model) (indexListItem, bool) {
	it, ok := m.indexList.SelectedItem().(indexListItem)
	return it, ok && !m.indexList.SettingFilter()
}
