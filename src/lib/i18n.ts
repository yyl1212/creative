export const locales = ['zh', 'en'] as const;
export type Locale = typeof locales[number];
export const copy = {
  zh: {
    tools: '工具目录', about: '关于', privacy: '隐私', feedback: '反馈', admin: '管理后台',
    skip: '跳到主要内容', home: '首页', tagline: '简单实用，解决具体问题。',
    hero: '实用工具，\n让事情更简单。',
    intro: '从真实需求出发，逐步构建可以直接使用的小工具。',
    catalog: '浏览工具目录', status: '工具准备中',
    empty: '暂无已上线工具',
    preparing: '工具完成验证并正式上线后，会显示在这里。',
    directoryDescription: '所有已上线工具，集中在这里。',
    available: '个已上线工具',
    note: '围绕实际问题', detail: '先确认需求，再开发具体、实用的功能。',
    stage: '如实展示进展', stageText: '目前处于初始阶段，还没有已上线工具或付费服务。',
    footer: '简单实用，解决具体问题。',
    back: '返回首页', overview: '了解 Creative', contact: '反馈渠道',
    aboutIntro: 'Creative 是一个正在建设的实用工具网站。',
    aboutDetail: '我们从真实需求出发，逐步开发可以自助使用的小工具。只有完成开发并正式上线的产品，才会出现在工具目录中。'
  },
  en: {
    tools: 'Tools', about: 'About', privacy: 'Privacy', feedback: 'Feedback', admin: 'Admin',
    skip: 'Skip to content', home: 'Home', tagline: 'Practical tools for everyday problems.',
    hero: 'Useful tools.\nLess effort.',
    intro: 'We start with real needs and build practical tools you can use yourself.',
    catalog: 'Browse tools', status: 'Tools in preparation',
    empty: 'No tools are available yet.',
    preparing: 'Tools will appear here once they are validated and launched.',
    directoryDescription: 'Every available tool, in one place.',
    available: 'tools available',
    note: 'Built around real needs', detail: 'Understand a specific problem, then build a practical solution.',
    stage: 'An honest view of progress', stageText: 'We are getting started. No tools or paid services are available yet.',
    footer: 'Practical tools for everyday problems.',
    back: 'Back to home', overview: 'About Creative', contact: 'Feedback channel',
    aboutIntro: 'Creative is a practical tool website in the making.',
    aboutDetail: 'We start with real needs and build focused, self-service tools. Only completed and launched products will appear in the directory.'
  }
};
