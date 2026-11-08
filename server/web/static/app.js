// Kleine Helfer, die Seite funktioniert auch ohne JavaScript.
document.addEventListener("click", (e) => {
  const b = e.target.closest("[data-confirm]");
  if (b && !confirm(b.dataset.confirm)) e.preventDefault();
  const c = e.target.closest("[data-copy]");
  if (c) {
    const t = document.getElementById(c.dataset.copy).textContent;
    navigator.clipboard.writeText(t).then(() => { c.textContent = "Kopiert ✓"; });
  }
});
document.querySelectorAll("[data-autosubmit]").forEach((el) =>
  el.addEventListener("change", () => el.form.submit()));
// Rechte sind für Admins egal – ausgrauen
const perms = document.querySelector("[data-perms]");
if (perms) {
  const sync = () => { perms.disabled = document.querySelector("input[name=role][value=admin]").checked; };
  document.querySelectorAll("input[name=role]").forEach((r) => r.addEventListener("change", sync));
  sync();
}
