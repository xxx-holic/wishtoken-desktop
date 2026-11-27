'use strict';
const LINKS = Object.freeze({
  chatgptSecurity: 'https://chatgpt.com/settings/security',
  website: 'https://wishtoapp.com/',
  shop: 'https://wishtoken.team/',
  liandong: 'https://wzyp.cn/shop/6YHGJOPN',
  dispatch: 'https://wishtoken.team/shop/dispatch',
  pricing: 'https://sub2api.wishtoapp.com/guide/channels?utm_source=wishtoken',
  project: 'https://github.com/xxx-holic/wishtoken-desktop',
  community: 'https://t.me/wishtoken_group',
  announcements: 'https://t.me/wishtoken_channel',
  contact: 'https://t.me/wishtoken_team',
  releases: 'https://github.com/xxx-holic/wishtoken-desktop/releases',
  invitations: 'https://github.com/xxx-holic/wishtoken-desktop/discussions/categories/announcements'
});
function externalLink(name) {
  if (typeof name !== 'string' || !Object.hasOwn(LINKS, name)) throw new Error('Unknown external destination');
  return LINKS[name];
}
const CODES = Object.freeze({ referral: 'r5tcjnab', qq: '1065568670' });
function businessCode(name) {
  if (typeof name !== 'string' || !Object.hasOwn(CODES, name)) throw new Error('Unknown business code');
  return CODES[name];
}
module.exports = { externalLink, businessCode };
