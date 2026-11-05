import build from 'virtual:version'

// Which build of the console this is (version.mjs): "v0.1.0 · 3646b34", and
// when it was built, for a tooltip.
export const versionLabel = ['v' + build.version, build.commit].filter(Boolean).join(' · ')
export const builtAt = `Built ${build.built.replace('T', ' ').replace('Z', ' UTC')}`
