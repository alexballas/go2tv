package filechooser

// ComboBox represents a serialized combo box that can be added to the dialog.
// None of the strings, except for the initial selection, should be empty.
// As a special case, passing an empty array for the list of choices indicates
// a boolean choice that is typically displayed as a check button, using “true” and “false” as the choices.
type ComboBox struct {
	ID               string   // An ID that will be returned with the response.
	Label            string   // A user-visible label.
	Choices          []Choice // The list of choices.
	InitialSelection string   // Default selection of choice. Leave empty to let the portal decide.
}

// Choice represent a choice that can be made in a ComboBox.
type Choice struct {
	ID    string // An ID that will be returned with the response.
	Label string // A user-visible label.
}
