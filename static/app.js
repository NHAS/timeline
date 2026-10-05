// Confirm destructive actions (any element with data-confirm).
document.addEventListener('click', (e) => {
  const el = e.target.closest('[data-confirm]');
  if (el && !window.confirm(el.dataset.confirm)) e.preventDefault();
});

// New-story form: only show the unit field for number-based timelines.
const unitRow = document.getElementById('unit-row');
if (unitRow) {
  const sync = () => {
    const checked = document.querySelector('input[name=mode]:checked');
    unitRow.hidden = !(checked && checked.value === 'number');
  };
  document.querySelectorAll('input[name=mode]').forEach((r) => r.addEventListener('change', sync));
  sync();
}

// Modal dialogs: any element with data-open-dialog="<id>" opens that <dialog>.
// Clicking the dark backdrop closes it (Escape and the Cancel button work natively).
document.addEventListener('click', (e) => {
  const opener = e.target.closest('[data-open-dialog]');
  if (!opener) return;
  const dialog = document.getElementById(opener.dataset.openDialog);
  if (dialog && typeof dialog.showModal === 'function') dialog.showModal();
});
document.querySelectorAll('dialog.modal').forEach((dialog) => {
  dialog.addEventListener('click', (e) => {
    if (e.target === dialog) dialog.close();
  });
});


const charactersearch = document.getElementById('character-search');
if (charactersearch) {
  charactersearch.addEventListener('input', () => {
    const q = charactersearch.value.trim().toLowerCase();
    document.querySelectorAll('[data-character-name]').forEach((el) => {
      el.hidden = q !== '' && !el.dataset.characterName.toLowerCase().includes(q);
    });
  });
}

const characterFilter = document.getElementById('character-filter');
const characterSelect = document.getElementById('character-select');
if (characterFilter && characterSelect) {
  // Remember the full option list; we rebuild the <select> from it on each keystroke
  // (hiding <option> elements isn't reliable across browsers).
  const allOptions = Array.from(characterSelect.options).map((o) => ({ value: o.value, text: o.textContent }));
 
  const applyFilter = () => {
    const q = characterFilter.value.trim().toLowerCase();
    const matches = allOptions.filter((o) => o.value !== '' && o.text.toLowerCase().includes(q));
 
    // Keep the current choice, unless you're searching and it no longer matches:
    // then jump to the first match so Tab/Save picks what you typed.
    let selected = characterSelect.value;
    if (q !== '' && !matches.some((o) => o.value === selected) && matches.length > 0) {
      selected = matches[0].value;
    }
 
    characterSelect.replaceChildren(
      ...allOptions
        .filter((o) => o.value === '' || o.value === selected || matches.includes(o))
        .map((o) => new Option(o.text, o.value))
    );
    characterSelect.value = selected;
  };
 
  characterFilter.addEventListener('input', applyFilter);
  // Enter in the search box shouldn't submit the whole form.
  characterFilter.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      characterSelect.focus();
    }
  });
}
