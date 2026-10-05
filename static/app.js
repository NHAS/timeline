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

const charactersearch = document.getElementById('character-search');
if (charactersearch) {
  charactersearch.addEventListener('input', () => {
    const q = charactersearch.value.trim().toLowerCase();
    document.querySelectorAll('[data-character-name]').forEach((el) => {
      el.hidden = q !== '' && !el.dataset.characterName.toLowerCase().includes(q);
    });
  });
}