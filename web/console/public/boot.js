// Runs before React: the saved language decides direction, so the first
// paint is already the right way round.
;(function () {
  try {
    var lang = localStorage.getItem('ga.console.lang') || 'en'
    document.documentElement.lang = lang
    document.documentElement.dir = lang === 'ar' ? 'rtl' : 'ltr'
  } catch {
    /* storage blocked: English, left to right */
  }
})()
