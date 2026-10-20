import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'

const en = {
  appName: 'Family Bank',
  nav: { accounts: 'Accounts', requests: 'Requests', settings: 'Settings' },
  common: { cancel: 'Cancel', send: 'Send', close: 'Close', loadMore: 'Load more', loading: 'Loading…', error: 'Something went wrong' },
  login: {
    familyCode: 'Family code', go: 'Go', who: 'Who are you?', noPersons: 'No one found for this family code.',
    password: 'Password', remember: 'Keep me logged in', logIn: 'Log in', change: 'Change family',
    tooMany: 'Too many attempts. Try again in {{minutes}} min.',
  },
  accounts: {
    title: 'Accounts', empty: 'No accounts yet.', pending: '+ {{amount}} pending',
    closed: 'Closed',
  },
  funding: {
    fundedToday: 'Funded today', skippedToday: 'Skipped today',
    turnOff: 'Turn off today', turnOn: 'Turn on today',
  },
  yes: 'Yes', no: 'No',
  account: {
    pending: '{{amount}} pending approval', available: 'Available: {{amount}}',
    deposit: 'Deposit', requestDeposit: 'Request deposit', withdraw: 'Withdraw', amount: 'Amount', comment: 'Note (optional)',
    pendingTitle: 'Pending', history: 'History', noHistory: 'No transactions yet.', autoFunding: 'Daily funding',
    deposited: 'Deposit posted', withdrawn: 'Withdrawal posted', requested: 'Request sent',
  },
  requests: {
    title: 'Requests', pending: 'Pending', all: 'All', approve: 'Approve', reject: 'Reject', none: 'No requests.',
    approved: 'Approved', rejected: 'Rejected', status: { pending: 'Pending', approved: 'Approved', rejected: 'Rejected' },
    rejectTitle: 'Reject request', approveTitle: 'Approve request', approveWithComment: 'Approve with comment…', moreApprove: 'More approve options', reason: 'Comment (optional)', requestedBy: 'Requested by {{name}} on {{date}}', requesterComment: 'Requester comment: {{comment}}', approverComment: 'Approver comment: {{comment}}',
  },
  settings: {
    title: 'Settings', connected: 'Connected:', dangerZone: 'Danger zone', dangerHint: 'Password and signed-in devices', language: 'Language', appearance: 'Appearance', light: 'Light', dark: 'Dark', auto: 'Auto', changePassword: 'Change password', current: 'Current password',
    next: 'New password (6 to 72 characters)', save: 'Save', passwordChanged: 'Password changed. Other devices were logged off.',
    devices: 'Devices', thisDevice: 'this device', revoke: 'Revoke', neverExpires: 'Stays logged in until revoked',
    expires: 'Expires {{date}}', logOff: 'Log off', unknownDevice: 'Unknown device',
  },
}

const fr: typeof en = {
  appName: 'Banque familiale',
  nav: { accounts: 'Comptes', requests: 'Demandes', settings: 'Réglages' },
  common: { cancel: 'Annuler', send: 'Envoyer', close: 'Fermer', loadMore: 'Voir plus', loading: 'Chargement…', error: 'Une erreur est survenue' },
  login: {
    familyCode: 'Code de famille', go: 'OK', who: 'Qui êtes-vous ?', noPersons: 'Personne trouvé pour ce code de famille.',
    password: 'Mot de passe', remember: 'Rester connecté', logIn: 'Se connecter', change: 'Changer de famille',
    tooMany: 'Trop de tentatives. Réessayez dans {{minutes}} min.',
  },
  accounts: {
    title: 'Comptes', empty: 'Aucun compte pour le moment.', pending: '+ {{amount}} en attente',
    closed: 'Fermé',
  },
  funding: {
    fundedToday: 'Alimenté aujourd’hui', skippedToday: 'Suspendu aujourd’hui',
    turnOff: 'Désactiver aujourd’hui', turnOn: 'Activer aujourd’hui',
  },
  yes: 'Oui', no: 'Non',
  account: {
    pending: '{{amount}} en attente d’approbation', available: 'Disponible : {{amount}}',
    deposit: 'Dépôt', requestDeposit: 'Demander un dépôt', withdraw: 'Retrait', amount: 'Montant', comment: 'Note (facultatif)',
    pendingTitle: 'En attente', history: 'Historique', noHistory: 'Aucune transaction.', autoFunding: 'Alimentation quotidienne',
    deposited: 'Dépôt effectué', withdrawn: 'Retrait effectué', requested: 'Demande envoyée',
  },
  requests: {
    title: 'Demandes', pending: 'En attente', all: 'Toutes', approve: 'Approuver', reject: 'Refuser', none: 'Aucune demande.',
    approved: 'Approuvée', rejected: 'Refusée', status: { pending: 'En attente', approved: 'Approuvée', rejected: 'Refusée' },
    rejectTitle: 'Refuser la demande', approveTitle: 'Approuver la demande', approveWithComment: 'Approuver avec un commentaire…', moreApprove: 'Plus d’options d’approbation', reason: 'Commentaire (facultatif)', requestedBy: 'Demandé par {{name}} le {{date}}', requesterComment: 'Commentaire du demandeur : {{comment}}', approverComment: 'Commentaire de l’approbateur : {{comment}}',
  },
  settings: {
    title: 'Réglages', connected: 'Connecté :', dangerZone: 'Zone sensible', dangerHint: 'Mot de passe et appareils connectés', language: 'Langue', appearance: 'Apparence', light: 'Clair', dark: 'Sombre', auto: 'Auto', changePassword: 'Changer le mot de passe', current: 'Mot de passe actuel',
    next: 'Nouveau mot de passe (6 à 72 caractères)', save: 'Enregistrer', passwordChanged: 'Mot de passe changé. Les autres appareils ont été déconnectés.',
    devices: 'Appareils', thisDevice: 'cet appareil', revoke: 'Révoquer', neverExpires: 'Reste connecté jusqu’à révocation',
    expires: 'Expire le {{date}}', logOff: 'Se déconnecter', unknownDevice: 'Appareil inconnu',
  },
}

export const LANG_KEY = 'vbank.lang'

const saved = localStorage.getItem(LANG_KEY)
const initial = saved ?? (navigator.language.toLowerCase().startsWith('fr') ? 'fr' : 'en')

void i18n.use(initReactI18next).init({
  resources: { en: { translation: en }, fr: { translation: fr } },
  lng: initial,
  fallbackLng: 'en',
  interpolation: { escapeValue: false },
})

export function setLang(lng: 'en' | 'fr') {
  localStorage.setItem(LANG_KEY, lng)
  void i18n.changeLanguage(lng)
  document.documentElement.lang = lng
}

export default i18n
