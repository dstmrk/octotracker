// Script di prova per i test di StaticHandler (golden Tornado).
document.addEventListener("DOMContentLoaded", () => {
  const p = document.querySelector("p");
  if (p) {
    p.dataset.pronto = "si";
  }
});
