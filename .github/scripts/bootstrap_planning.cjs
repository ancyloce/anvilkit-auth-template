const fs = require('fs');

function parseIssueBlocks(md) {
  const lines = md.split(/\r?\n/);
  const blocks = [];

  for (let i = 0; i < lines.length; i += 1) {
    const issueMatch = lines[i].match(/^### Issue: (.+)$/);
    if (!issueMatch) continue;

    const title = issueMatch[1].trim();
    let labels = [];
    let bodyStart = i + 1;

    if (lines[i + 1] && lines[i + 1].startsWith('**Labels**:')) {
      const raw = lines[i + 1].replace('**Labels**:', '').trim();
      labels = raw
        .replace(/`/g, '')
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
      bodyStart = i + 2;
    }

    let bodyEnd = lines.length;
    for (let j = bodyStart; j < lines.length; j += 1) {
      if (lines[j].startsWith('### Issue: ')) {
        bodyEnd = j;
        break;
      }
    }

    const body = lines.slice(bodyStart, bodyEnd).join('\n').trim();
    blocks.push({ title, labels, body });
    i = bodyEnd - 1;
  }

  return blocks;
}

async function run({ github, context, core, processEnv }) {
  const env = processEnv || process.env;
  const owner = context.repo.owner;
  const repo = context.repo.repo;
  const plannerPath = env.ISSUE_MD_PATH || 'docs/issues/m4-m6.md';
  const dryRun = String(env.DRY_RUN || 'false') === 'true';

  if (!fs.existsSync(plannerPath)) {
    core.warning(`${plannerPath} not found, skip bootstrap.`);
    return;
  }

  const markdown = fs.readFileSync(plannerPath, 'utf8');
  const issues = parseIssueBlocks(markdown);
  if (issues.length === 0) {
    core.warning(`No issue definitions found in ${plannerPath}`);
    return;
  }

  const milestoneSpecs = {
    M1: {
      title: 'M1 - AuthN Core',
      description: '完成注册、登录、刷新、登出、JWT 中间件、测试与 CI gating，形成可上线的认证最小闭环。',
    },
    M2: {
      title: 'M2 - Multi-tenant MVP',
      description: '引入租户数据模型、Bootstrap、租户切换与成员管理基础能力。',
    },
    M3: {
      title: 'M3 - RBAC with Casbin',
      description: '完成基于租户域（tid）的 Casbin 鉴权，覆盖默认策略、角色映射、中间件与测试。',
    },
    M4: {
      title: 'M4 - Email Service Core',
      description: '完成邮件服务基础设施：数据库表结构、Redis 队列、email-worker 服务、SMTP 集成，形成可发送邮件的最小闭环。',
    },
    M5: {
      title: 'M5 - Email Verification MVP',
      description: '实现完整的邮件验证流程：OTP + Magic Link 双验证机制、注册/登录流程改造、防滥用限流、同设备检测。',
    },
    M6: {
      title: 'M6 - Email Service Advanced',
      description: '完成生产级邮件服务：邮件送达率优化（SPF/DKIM/DMARC）、Bounce 处理、Webhook 集成、分析埋点。',
    },
  };

  if (dryRun) {
    core.info(`[dry-run] parsed issues: ${issues.length}`);
    for (const issue of issues) {
      core.info(`[dry-run] issue title: ${issue.title}`);
    }
    return;
  }

  async function ensureMilestone(title, description) {
    const list = await github.paginate(github.rest.issues.listMilestones, {
      owner,
      repo,
      state: 'all',
      per_page: 100,
    });

    const found = list.find((m) => m.title === title);
    if (found) {
      core.info(`Milestone exists: ${title} (#${found.number})`);
      return found;
    }

    const created = await github.rest.issues.createMilestone({
      owner,
      repo,
      title,
      description,
      state: 'open',
    });
    core.info(`Milestone created: ${title} (#${created.data.number})`);
    return created.data;
  }

  const milestoneByKey = {};
  for (const [key, spec] of Object.entries(milestoneSpecs)) {
    milestoneByKey[key] = await ensureMilestone(spec.title, spec.description);
  }

  // Ensure all required labels exist
  async function ensureLabels(labelNames) {
    const existingLabels = await github.paginate(github.rest.issues.listLabelsForRepo, {
      owner,
      repo,
      per_page: 100,
    });

    const existingLabelNames = new Set(existingLabels.map((l) => l.name));
    const labelsToCreate = labelNames.filter((name) => !existingLabelNames.has(name));

    for (const labelName of labelsToCreate) {
      try {
        await github.rest.issues.createLabel({
          owner,
          repo,
          name: labelName,
          color: 'ededed',
          description: `Auto-created by ${context.workflow}`,
        });
        core.info(`Label created: ${labelName}`);
      } catch (error) {
        if (error.status !== 422) {
          core.warning(`Failed to create label ${labelName}: ${error.message}`);
        }
      }
    }
  }

  // Collect all unique labels from issues
  const allLabels = new Set();
  for (const issue of issues) {
    for (const label of issue.labels) {
      allLabels.add(label);
    }
  }
  await ensureLabels(Array.from(allLabels));

  const existingIssues = await github.paginate(github.rest.issues.listForRepo, {
    owner,
    repo,
    state: 'all',
    per_page: 100,
  });

  const existingByTitle = new Map();
  for (const issue of existingIssues) {
    if (!issue.pull_request) {
      existingByTitle.set(issue.title, issue);
    }
  }

  const created = [];
  const skipped = [];

  for (const issue of issues) {
    const matched = issue.title.match(/^(M[1-6])-\d+/);
    if (!matched) {
      core.warning(`Skip issue without milestone prefix: ${issue.title}`);
      continue;
    }

    const milestone = milestoneByKey[matched[1]];
    if (existingByTitle.has(issue.title)) {
      const existing = existingByTitle.get(issue.title);
      skipped.push(`- ${issue.title} (#${existing.number})`);
      continue;
    }

    const body = `${issue.body}\n\n---\n_This issue was bootstrapped by workflow \`${context.workflow}\` from \`${plannerPath}\`._`;
    try {
      const result = await github.rest.issues.create({
        owner,
        repo,
        title: issue.title,
        body,
        labels: issue.labels,
        milestone: milestone.number,
      });

      existingByTitle.set(issue.title, result.data);
      created.push(`- ${issue.title} (#${result.data.number})`);
    } catch (error) {
      core.error(`Failed to create issue "${issue.title}": ${error.message}`);
      skipped.push(`- ${issue.title} (failed: ${error.message})`);
    }
  }

  const marker = '<!-- planning-bootstrap:m4-m6 -->';
  const summary = [
    marker,
    '### Planning bootstrap result',
    '',
    `Source: \`${plannerPath}\``,
    '',
    '**Created**',
    ...(created.length ? created : ['- none']),
    '',
    '**Skipped (already exists)**',
    ...(skipped.length ? skipped : ['- none']),
  ].join('\n');

  const eventName = context.eventName;
  let issueNumber = context.issue.number;
  if (!issueNumber && env.PR_NUMBER) {
    issueNumber = Number(env.PR_NUMBER);
  }

  if (eventName === 'pull_request' || issueNumber) {
    const comments = await github.paginate(github.rest.issues.listComments, {
      owner,
      repo,
      issue_number: issueNumber,
      per_page: 100,
    });

    const ownComment = comments.find(
      (c) => c.user?.type === 'Bot' && c.body?.includes(marker),
    );

    if (ownComment) {
      await github.rest.issues.updateComment({
        owner,
        repo,
        comment_id: ownComment.id,
        body: summary,
      });
    } else {
      await github.rest.issues.createComment({
        owner,
        repo,
        issue_number: issueNumber,
        body: summary,
      });
    }
  } else {
    core.info('Non-PR event detected; skip PR summary comment.');
  }
}

module.exports = { run, parseIssueBlocks };

if (require.main === module) {
  const plannerPath = process.env.ISSUE_MD_PATH || 'docs/issues/m4-m6.md';
  const content = fs.readFileSync(plannerPath, 'utf8');
  const issues = parseIssueBlocks(content);
  console.log(`[local] parsed ${issues.length} issue templates from ${plannerPath}`);
  for (const issue of issues) {
    console.log(`[local] ${issue.title}`);
  }
}
