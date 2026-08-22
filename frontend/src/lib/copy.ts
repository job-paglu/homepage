/**
 * Every user-facing string on the public site.
 *
 * Centralised so internationalisation is a lookup change rather than a hunt
 * through markup (project CLAUDE.md, *Extension Points*). Components must not
 * contain literal prose — if a reviewer finds an English sentence in a
 * `.astro` template, it belongs here instead.
 *
 * `as const` is deliberate: it makes every value a readonly literal, so a typo
 * in a key is a compile error and no component can mutate shared copy.
 */

export const copy = {
  site: {
    name: 'Job Paglu',
    /** The wordmark is set in two tones, so it needs the two words separately. */
    wordmarkFirst: 'Job',
    wordmarkSecond: 'Paglu',
    /** og:locale wants the underscore form, not the BCP-47 hyphen form. */
    ogLocale: 'en_US',
    htmlLang: 'en',
    tagline: 'International jobs, one clean list.',
  },

  nav: {
    skipToContent: 'Skip to main content',
    primaryLabel: 'Primary',
    footerLabel: 'Footer',
    home: 'Home',
    jobs: 'Jobs',
    privacy: 'Privacy',
    terms: 'Terms',
    openMenu: 'Open menu',
    closeMenu: 'Close menu',
    menuLabel: 'Site menu',
    logoHome: 'Job Paglu — home',
    /**
     * Link to the apex site, which carries its own job links. Short enough for
     * the header at 900px, where space is tight.
     */
    jobLinks: 'Job Links',
    /**
     * Accessible name. Says the destination and that it opens a new tab, because
     * "Job Links" alone tells a screen-reader user neither. Descriptive link
     * text is a WCAG 2.1 requirement the project committed to.
     */
    jobLinksAria: 'Job Links on jobpaglu.eu.cc — opens in a new tab',
  },

  home: {
    metaTitle: 'Find International Jobs — Work From Anywhere',
    metaDescription:
      'Discover international employment opportunities on Job Paglu. Browse remote, hybrid and on-site roles from companies hiring across borders, and apply directly with the employer.',
    heroHeadline: 'Find International Jobs. Work From Anywhere.',
    /** The same headline in two parts, so the second sentence can carry the
     *  brand colour. Keep the concatenation equal to `heroHeadline`. */
    heroHeadlineLead: 'Find International Jobs.',
    heroHeadlineAccent: 'Work From Anywhere.',
    heroEyebrow: 'Worldwide opportunities',
    heroSupport:
      'Job Paglu is a focused place to discover international employment opportunities — roles that hire across borders, remote-first teams, and relocation-friendly employers. No accounts, no noise: read the role, then apply directly with the company.',
    heroPrimaryCta: 'Explore Jobs',
    heroSecondaryCta: 'Browse Latest Jobs',
    latestHeading: 'Latest international roles',
    latestSupport: 'The most recently published openings. Featured roles appear first.',
    latestAllLink: 'View all jobs',
  },

  jobsIndex: {
    metaTitle: 'International Jobs',
    metaDescription:
      'Every international job currently published on Job Paglu — remote, hybrid and on-site roles, newest first, with a direct link to each employer’s own application page.',
    heading: 'International jobs',
    support: 'Every currently published opening. Newest first, featured roles ahead of the rest.',
    /** Rendered as e.g. "12 jobs · page 1 of 2". */
    countOne: 'job',
    countMany: 'jobs',
    pageOf: 'Page',
    pageOfJoin: 'of',
  },

  job: {
    /** Card action, primary-quiet. Needs the job title appended for screen readers. */
    view: 'View Job',
    viewSuffix: 'View details for',
    apply: 'Apply Now',
    applyPanelHeading: 'Ready to apply?',
    applyPanelNote:
      'Applications are handled entirely by the employer on their own site. Job Paglu does not receive or store your application.',
    applySuffix: 'Apply for',
    applyExternalNote: 'Opens the employer’s own application page in a new tab.',
    applyUnavailable: 'Application link unavailable',
    applyUnavailableDetail:
      'The employer’s application link for this role did not pass our safety check, so we will not open it. Please try again later.',
    featured: 'Featured',
    labelLocation: 'Location',
    labelJobType: 'Job type',
    labelSalary: 'Salary',
    labelQualification: 'Qualification',
    labelCompany: 'Company',
    labelCountry: 'Country',
    labelWorkStyle: 'Work style',
    labelExperience: 'Experience',
    labelCategory: 'Category',
    labelPosted: 'Posted',
    labelClosing: 'Applications close',
    descriptionHeading: 'About this role',
    detailsHeading: 'Role details',
    notSpecified: 'Not specified',
    backToJobs: 'Back to all jobs',
    metaDescriptionFallback: 'An international job opportunity published on Job Paglu.',
  },

  states: {
    emptyTitle: 'No jobs available right now',
    emptyBody: 'New international opportunities will appear here when published.',
    emptyAction: 'Back to home',
    errorTitle: 'We could not load jobs',
    errorRetry: 'Try again',
    errorRequestIdPrefix: 'Reference',
    errorFallbackMessage: 'Something went wrong while loading this page.',
    loadingLabel: 'Loading jobs',
  },

  pagination: {
    label: 'Job list pages',
    previous: 'Previous',
    next: 'Next',
    pagePrefix: 'Page',
    currentSuffix: 'current page',
  },

  popup: {
    eyebrow: 'Announcement',
    heading: 'International Jobs — Currently FREE',
    body: 'Job Paglu currently provides access to international job opportunities for free. This service may become regulated and paid in the future. Currently, Job Paglu is focused on international job opportunities only.',
    close: '× Close',
    closeLabel: 'Close announcement',
  },

  notFound: {
    metaTitle: 'Page not found',
    metaDescription: 'That page does not exist on Job Paglu. Browse currently published international jobs instead.',
    heading: 'That page does not exist',
    body: 'The link may be out of date, or the job may have been unpublished or filled. Everything currently open is on the jobs page.',
    action: 'Browse international jobs',
    secondaryAction: 'Go to home',
  },

  privacy: {
    metaTitle: 'Privacy',
    metaDescription:
      'How Job Paglu handles visitor information today, and the status of our full privacy policy.',
    heading: 'Privacy',
    /** Deliberately claims nothing that is not true — no GDPR claim, no entity, no address. */
    paragraphs: [
      'A full privacy policy for Job Paglu is being prepared and is not published yet. This page describes, plainly, what the site does today.',
      'Browsing Job Paglu does not require an account, and the public pages do not ask you for a name, an email address, or any other personal detail. There is no sign-up form, no newsletter, and no advertising or analytics tracker embedded in these pages.',
      'A dismissal preference for the site announcement is stored in your own browser (in localStorage) so the notice does not reappear on every visit. It stays on your device, is not sent anywhere, and clearing your browser storage removes it.',
      'Applying for a role takes you to the employer’s own website. Anything you enter there is handled by that employer under their policy, not ours — Job Paglu does not receive or store your application.',
      'Our hosting and content-delivery providers process ordinary technical request data (such as IP address and user agent) in order to serve the site and protect it from abuse. That is a normal part of serving a website and is not used by us to build a profile of you.',
    ],
    contactHeading: 'Questions',
    contactBody:
      'If you have a privacy question before the full policy is published, please reach out through the contact route published with the site. This page will be replaced by the complete policy once it is ready.',
  },

  terms: {
    metaTitle: 'Terms',
    metaDescription: 'The current status of Job Paglu’s terms of use, and what the site does and does not promise today.',
    heading: 'Terms',
    paragraphs: [
      'Full terms of use for Job Paglu are being prepared and are not published yet. Until then, the following describes how the site is intended to be used.',
      'Job Paglu lists international job opportunities and links to the employer’s own application page. We are not the employer, not a recruiter, and not a party to any hiring decision, offer, contract, or payment.',
      'Listings are supplied by the people who publish them. We do not independently verify salaries, titles, requirements, or the legal status of any employer, and a role may be filled or withdrawn before a listing is removed. Treat every listing as a starting point for your own checks.',
      'Access to international job listings is currently provided free of charge. This may change in the future, and this page will be updated before it does.',
      'The site is provided as-is. Please do not scrape, republish, or resell the listings.',
    ],
    contactHeading: 'Questions',
    contactBody:
      'For anything about these terms before the full version is published, please use the contact route published with the site.',
  },

  footer: {
    blurb: 'A focused, ad-free place to discover international job opportunities.',
    exploreHeading: 'Explore',
    legalHeading: 'Legal',
    freeNotice: 'Access to international job listings is currently free.',
    /** Year is appended at render time. */
    copyrightPrefix: '©',
    copyrightSuffix: 'Job Paglu',
  },
} as const;

export default copy;
