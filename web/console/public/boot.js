// Runs before React, so the first paint is already right: the saved language
// decides direction, and the saved theme (the rail's switch) the colours.
// No saved theme follows the system.
;(function () {
  try {
    var lang = localStorage.getItem('ga.console.lang') || 'en'
    document.documentElement.lang = lang
    document.documentElement.dir = lang === 'ar' ? 'rtl' : 'ltr'
    var theme = localStorage.getItem('ga.console.theme')
    if (theme === 'light' || theme === 'dark') document.documentElement.dataset.theme = theme
  } catch {
    /* storage blocked: English, left to right, the system's theme */
  }
})()
