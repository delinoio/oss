//! Differential test of the completion tracking in `env_guard`.
//!
//! The scan decides which `return process.env.NAME` statements of a function
//! can supply its result. The tests generate statement trees over `try`,
//! `catch`, `finally`, `switch`, labels, loops and `if` with `return`,
//! `throw`, `break` and `continue`, print them as JavaScript, and compare the
//! reads that the scan reports with the returns that an independent reference
//! can reach. The reference executes the tree with the ECMAScript completion
//! records (normal, return, throw, break, continue), exploring every
//! combination of the unknown choices (a condition, a thrown expression, the
//! matching `case`). It shares no code with the scan, so a rule that the scan
//! gets wrong in one construct shows up as a differing program here.
//!
//! The abstraction is the one the scan documents: every evaluation of a
//! non-literal expression may throw, a loop test is unknown (an endless loop
//! has none or a literal `true`), and no state carries between iterations.

use std::collections::HashSet;

use super::{super::non_code_ranges, EnvGuard, env_guards};

// ---------------------------------------------------------------------------
// Programs
// ---------------------------------------------------------------------------

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum LoopKind {
    /// `while(y)`
    While,
    /// `while(true)`
    WhileTrue,
    /// `for(;;)`
    ForEver,
    /// `for(;y;)`
    ForTest,
    /// `for(;true;)`
    ForTrue,
    /// `do S while(y)`
    Do,
    /// `do S while(true)`
    DoTrue,
}

impl LoopKind {
    const ALL: [Self; 7] = [
        Self::While,
        Self::WhileTrue,
        Self::ForEver,
        Self::ForTest,
        Self::ForTrue,
        Self::Do,
        Self::DoTrue,
    ];

    fn endless(self) -> bool {
        matches!(
            self,
            Self::WhileTrue | Self::ForEver | Self::ForTrue | Self::DoTrue
        )
    }

    /// The test runs before the first iteration.
    fn tests_first(self) -> bool {
        !matches!(self, Self::Do | Self::DoTrue)
    }
}

#[derive(Clone, Debug)]
enum Clause {
    Case,
    Default,
}

#[derive(Clone, Debug)]
enum Stmt {
    Empty,
    /// `return process.env.R<n>;`
    ReturnRead(usize),
    /// `return 1;`
    ReturnLiteral,
    /// `return;`
    ReturnBare,
    /// `throw 1;`
    Throw,
    /// `break;` or `break L<n>;`
    Break(Option<usize>),
    /// `continue;` or `continue L<n>;`
    Continue(Option<usize>),
    /// `f();`
    Call,
    /// `if(y)then else other`
    If(Box<Stmt>, Option<Box<Stmt>>),
    Block(Vec<Stmt>),
    Try {
        block: Vec<Stmt>,
        /// `Some(true)` is `catch(e){}`, `Some(false)` is `catch{}`.
        catch: Option<(bool, Vec<Stmt>)>,
        finally: Option<Vec<Stmt>>,
    },
    Loop(LoopKind, Box<Stmt>),
    Switch(Vec<(Clause, Vec<Stmt>)>),
    Labelled(usize, Box<Stmt>),
}

/// Whether `stmt` or a statement nested in it satisfies `test`.
fn any(stmt: &Stmt, test: &dyn Fn(&Stmt) -> bool) -> bool {
    let any_list = |list: &[Stmt]| list.iter().any(|stmt| any(stmt, test));
    test(stmt)
        || match stmt {
            Stmt::If(then, other) => {
                any(then, test) || other.as_ref().is_some_and(|other| any(other, test))
            }
            Stmt::Block(list) => any_list(list),
            Stmt::Try {
                block,
                catch,
                finally,
            } => {
                any_list(block)
                    || catch.as_ref().is_some_and(|(_, list)| any_list(list))
                    || finally.as_ref().is_some_and(|list| any_list(list))
            }
            Stmt::Loop(_, body) | Stmt::Labelled(_, body) => any(body, test),
            Stmt::Switch(clauses) => clauses.iter().any(|(_, list)| any_list(list)),
            _ => false,
        }
}

fn render_list(list: &[Stmt], out: &mut String) {
    for stmt in list {
        render(stmt, out);
    }
}

fn render_branch(stmt: &Stmt, out: &mut String) {
    // A nested `if` without `else` would capture a following `else`.
    if matches!(
        stmt,
        Stmt::If(..) | Stmt::Loop(..) | Stmt::Labelled(..) | Stmt::Try { .. }
    ) {
        out.push('{');
        render(stmt, out);
        out.push('}');
    } else {
        render(stmt, out);
    }
}

fn render(stmt: &Stmt, out: &mut String) {
    match stmt {
        Stmt::Empty => out.push(';'),
        Stmt::ReturnRead(read) => out.push_str(&format!("return process.env.R{read};")),
        Stmt::ReturnLiteral => out.push_str("return 1;"),
        Stmt::ReturnBare => out.push_str("return;"),
        Stmt::Throw => out.push_str("throw 1;"),
        Stmt::Break(None) => out.push_str("break;"),
        Stmt::Break(Some(label)) => out.push_str(&format!("break L{label};")),
        Stmt::Continue(None) => out.push_str("continue;"),
        Stmt::Continue(Some(label)) => out.push_str(&format!("continue L{label};")),
        Stmt::Call => out.push_str("f();"),
        Stmt::If(then, other) => {
            out.push_str("if(y)");
            render_branch(then, out);
            if let Some(other) = other {
                out.push_str("else ");
                render(other, out);
            }
        }
        Stmt::Block(list) => {
            out.push('{');
            render_list(list, out);
            out.push('}');
        }
        Stmt::Try {
            block,
            catch,
            finally,
        } => {
            out.push_str("try{");
            render_list(block, out);
            out.push('}');
            if let Some((binding, list)) = catch {
                out.push_str(if *binding { "catch(e){" } else { "catch{" });
                render_list(list, out);
                out.push('}');
            }
            if let Some(list) = finally {
                out.push_str("finally{");
                render_list(list, out);
                out.push('}');
            }
        }
        Stmt::Loop(kind, body) => match kind {
            LoopKind::While => {
                out.push_str("while(y)");
                render(body, out);
            }
            LoopKind::WhileTrue => {
                out.push_str("while(true)");
                render(body, out);
            }
            LoopKind::ForEver => {
                out.push_str("for(;;)");
                render(body, out);
            }
            LoopKind::ForTest => {
                out.push_str("for(;y;)");
                render(body, out);
            }
            LoopKind::ForTrue => {
                out.push_str("for(;true;)");
                render(body, out);
            }
            LoopKind::Do | LoopKind::DoTrue => {
                out.push_str("do ");
                render(body, out);
                out.push_str(if *kind == LoopKind::Do {
                    "while(y);"
                } else {
                    "while(true);"
                });
            }
        },
        Stmt::Switch(clauses) => {
            out.push_str("switch(y){");
            for (index, (clause, list)) in clauses.iter().enumerate() {
                match clause {
                    Clause::Case => out.push_str(&format!("case {index}:")),
                    Clause::Default => out.push_str("default:"),
                }
                render_list(list, out);
            }
            out.push('}');
        }
        Stmt::Labelled(label, body) => {
            out.push_str(&format!("L{label}:"));
            render(body, out);
        }
    }
}

// ---------------------------------------------------------------------------
// Reference semantics
// ---------------------------------------------------------------------------

/// A completion record. `Cut` stands for a run that never ends (an endless
/// loop after the iteration bound); it runs no `finally` block.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Completion {
    Normal,
    Return(Option<usize>),
    Throw,
    Break(Option<usize>),
    Continue(Option<usize>),
    Cut,
}

/// The iterations of a loop that are told apart. The unknown choices do not
/// depend on earlier iterations, so a behaviour that needs more is reachable
/// within two.
const MAX_ITERATIONS: usize = 2;

/// Enumerates every combination of choices by depth-first replay.
#[derive(Default)]
struct Explorer {
    script: Vec<(usize, usize)>,
    cursor: usize,
    reached: HashSet<usize>,
}

impl Explorer {
    fn choose(&mut self, arity: usize) -> usize {
        if let Some(&(choice, _)) = self.script.get(self.cursor) {
            self.cursor += 1;
            choice
        } else {
            self.script.push((0, arity));
            self.cursor += 1;
            0
        }
    }

    fn flip(&mut self) -> bool {
        while let Some((choice, arity)) = self.script.pop() {
            if choice + 1 < arity {
                self.script.push((choice + 1, arity));
                return true;
            }
        }
        false
    }

    /// Evaluating an expression that is not a literal may throw.
    fn evaluate(&mut self) -> Option<Completion> {
        (self.choose(2) == 1).then_some(Completion::Throw)
    }

    fn list(&mut self, list: &[Stmt]) -> Completion {
        for stmt in list {
            match self.exec(stmt, &[]) {
                Completion::Normal => {}
                other => return other,
            }
        }
        Completion::Normal
    }

    fn exec(&mut self, stmt: &Stmt, labels: &[usize]) -> Completion {
        match stmt {
            Stmt::Empty => Completion::Normal,
            Stmt::ReturnRead(read) => self.evaluate().unwrap_or(Completion::Return(Some(*read))),
            Stmt::ReturnLiteral | Stmt::ReturnBare => Completion::Return(None),
            Stmt::Throw => Completion::Throw,
            Stmt::Break(label) => Completion::Break(*label),
            Stmt::Continue(label) => Completion::Continue(*label),
            Stmt::Call => self.evaluate().unwrap_or(Completion::Normal),
            Stmt::If(then, other) => {
                if let Some(thrown) = self.evaluate() {
                    return thrown;
                }
                if self.choose(2) == 1 {
                    self.exec(then, &[])
                } else if let Some(other) = other {
                    self.exec(other, &[])
                } else {
                    Completion::Normal
                }
            }
            Stmt::Block(list) => self.list(list),
            Stmt::Try {
                block,
                catch,
                finally,
            } => {
                let mut completion = self.list(block);
                if completion == Completion::Cut {
                    return completion;
                }
                if completion == Completion::Throw
                    && let Some((_, handler)) = catch
                {
                    completion = self.list(handler);
                    if completion == Completion::Cut {
                        return completion;
                    }
                }
                if let Some(cleanup) = finally {
                    let replaced = self.list(cleanup);
                    if replaced != Completion::Normal {
                        completion = replaced;
                    }
                }
                completion
            }
            Stmt::Loop(kind, body) => self.run_loop(*kind, body, labels),
            Stmt::Switch(clauses) => {
                if let Some(thrown) = self.evaluate() {
                    return thrown;
                }
                let has_default = clauses
                    .iter()
                    .any(|(clause, _)| matches!(clause, Clause::Default));
                // Any clause can be the first to run; with no `default` a
                // value may also match none.
                let start = self.choose(clauses.len() + usize::from(!has_default));
                for (_, list) in clauses.iter().skip(start) {
                    match self.list(list) {
                        Completion::Normal => {}
                        Completion::Break(None) => return Completion::Normal,
                        other => return other,
                    }
                }
                Completion::Normal
            }
            Stmt::Labelled(label, body) => {
                let mut attached = labels.to_vec();
                attached.push(*label);
                match self.exec(body, &attached) {
                    Completion::Break(Some(target)) if target == *label => Completion::Normal,
                    other => other,
                }
            }
        }
    }

    fn run_loop(&mut self, kind: LoopKind, body: &Stmt, labels: &[usize]) -> Completion {
        let mut iterations = 0;
        loop {
            if kind.tests_first()
                && let Some(done) = self.loop_test(kind, iterations)
            {
                return done;
            }
            iterations += 1;
            match self.exec(body, &[]) {
                Completion::Normal | Completion::Continue(None) => {}
                Completion::Continue(Some(label)) if labels.contains(&label) => {}
                Completion::Break(None) => return Completion::Normal,
                other => return other,
            }
            if !kind.tests_first()
                && let Some(done) = self.loop_test(kind, iterations)
            {
                return done;
            }
        }
    }

    /// Evaluate the test of a loop that has run `iterations` times. `None`
    /// runs the body again; `Some(Normal)` ends the loop; any other value ends
    /// the statement.
    fn loop_test(&mut self, kind: LoopKind, iterations: usize) -> Option<Completion> {
        if kind.endless() {
            return (iterations >= MAX_ITERATIONS).then_some(Completion::Cut);
        }
        if let Some(thrown) = self.evaluate() {
            return Some(thrown);
        }
        if iterations >= MAX_ITERATIONS || self.choose(2) == 0 {
            Some(Completion::Normal)
        } else {
            None
        }
    }
}

/// The reads that a function with this body can return.
fn returnable_reads(body: &[Stmt]) -> HashSet<usize> {
    let mut explorer = Explorer::default();
    loop {
        explorer.cursor = 0;
        if let Completion::Return(Some(read)) = explorer.list(body) {
            explorer.reached.insert(read);
        }
        if !explorer.flip() {
            return explorer.reached;
        }
    }
}

// ---------------------------------------------------------------------------
// The scan under test
// ---------------------------------------------------------------------------

/// The reads that the scan reports as controlling the `&&` after a call of the
/// function with this body, in the order of the reads in the text.
fn guarded_reads(body: &[Stmt]) -> (String, HashSet<usize>) {
    let mut code = String::from("(function(){");
    render_list(body, &mut code);
    code.push_str("})()&&run()");
    let mut reads = Vec::new();
    let mut names = Vec::new();
    let mut from = 0;
    while let Some(offset) = code[from..].find("process.env.R") {
        let start = from + offset;
        let digits = code[start + "process.env.R".len()..]
            .bytes()
            .take_while(u8::is_ascii_digit)
            .count();
        let end = start + "process.env.R".len() + digits;
        names.push(
            code[start + "process.env.R".len()..end]
                .parse::<usize>()
                .unwrap(),
        );
        reads.push((start, end));
        from = end;
    }
    let guards = env_guards(&code, &reads, &non_code_ranges(&code));
    let guarded = names
        .into_iter()
        .zip(guards)
        .filter(|(_, guard)| *guard == Some(EnvGuard::ShortCircuitAnd))
        .map(|(name, _)| name)
        .collect();
    (code, guarded)
}

/// Compare one program; returns a description of a difference.
fn difference(body: &[Stmt]) -> Option<String> {
    let expected = returnable_reads(body);
    let (code, actual) = guarded_reads(body);
    if expected == actual {
        return None;
    }
    let mut want: Vec<_> = expected.into_iter().collect();
    let mut got: Vec<_> = actual.into_iter().collect();
    want.sort_unstable();
    got.sort_unstable();
    Some(format!(
        "{code}\n  scan reports {got:?}, reference reaches {want:?}"
    ))
}

// ---------------------------------------------------------------------------
// Generators
// ---------------------------------------------------------------------------

struct Rng(u64);

impl Rng {
    fn next(&mut self) -> u64 {
        // SplitMix64.
        self.0 = self.0.wrapping_add(0x9e37_79b9_7f4a_7c15);
        let mut z = self.0;
        z = (z ^ (z >> 30)).wrapping_mul(0xbf58_476d_1ce4_e5b9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94d0_49bb_1331_11eb);
        z ^ (z >> 31)
    }

    fn below(&mut self, bound: usize) -> usize {
        (self.next() % bound as u64) as usize
    }

    fn chance(&mut self, numerator: usize, denominator: usize) -> bool {
        self.below(denominator) < numerator
    }
}

/// What a `break` or `continue` at the current position can name.
#[derive(Clone, Default)]
struct Scope {
    labels: Vec<usize>,
    loop_labels: Vec<usize>,
    in_loop: bool,
    in_breakable: bool,
}

struct Generator {
    rng: Rng,
    next_read: usize,
    next_label: usize,
}

impl Generator {
    fn new(seed: u64) -> Self {
        Self {
            rng: Rng(seed),
            next_read: 0,
            next_label: 0,
        }
    }

    fn leaf(&mut self, scope: &Scope) -> Stmt {
        loop {
            match self.rng.below(10) {
                0 | 1 => {
                    self.next_read += 1;
                    return Stmt::ReturnRead(self.next_read);
                }
                2 => return Stmt::ReturnLiteral,
                3 => return Stmt::ReturnBare,
                4 => return Stmt::Throw,
                5 => return Stmt::Call,
                6 => return Stmt::Empty,
                7 => {
                    if self.rng.chance(1, 2) && !scope.labels.is_empty() {
                        let label = scope.labels[self.rng.below(scope.labels.len())];
                        return Stmt::Break(Some(label));
                    }
                    if scope.in_breakable {
                        return Stmt::Break(None);
                    }
                }
                _ => {
                    if self.rng.chance(1, 2) && !scope.loop_labels.is_empty() {
                        let label = scope.loop_labels[self.rng.below(scope.loop_labels.len())];
                        return Stmt::Continue(Some(label));
                    }
                    if scope.in_loop {
                        return Stmt::Continue(None);
                    }
                }
            }
        }
    }

    fn list(&mut self, depth: usize, scope: &Scope) -> Vec<Stmt> {
        let len = match self.rng.below(8) {
            0 => 0,
            1..=3 => 1,
            4..=6 => 2,
            _ => 3,
        };
        (0..len).map(|_| self.stmt(depth, scope)).collect()
    }

    fn stmt(&mut self, depth: usize, scope: &Scope) -> Stmt {
        if depth == 0 {
            return self.leaf(scope);
        }
        match self.rng.below(16) {
            0..=4 => self.leaf(scope),
            5 | 6 => {
                let then = self.stmt(depth - 1, scope);
                let other = self
                    .rng
                    .chance(1, 2)
                    .then(|| Box::new(self.stmt(depth - 1, scope)));
                Stmt::If(Box::new(then), other)
            }
            7 => Stmt::Block(self.list(depth - 1, scope)),
            8..=10 => self.try_statement(depth, scope),
            11 | 12 => self.loop_statement(depth, scope, &[]),
            13 => {
                let mut inner = scope.clone();
                inner.in_breakable = true;
                let count = self.rng.below(4);
                let mut clauses = Vec::new();
                let default = self.rng.chance(2, 3).then(|| self.rng.below(count + 1));
                for index in 0..count + usize::from(default.is_some()) {
                    let clause = if default == Some(index) {
                        Clause::Default
                    } else {
                        Clause::Case
                    };
                    clauses.push((clause, self.list(depth - 1, &inner)));
                }
                Stmt::Switch(clauses)
            }
            _ => self.labelled(depth, scope),
        }
    }

    fn try_statement(&mut self, depth: usize, scope: &Scope) -> Stmt {
        let block = self.list(depth - 1, scope);
        let (catch, finally) = match self.rng.below(5) {
            0 => (true, false),
            1 => (false, true),
            _ => (true, true),
        };
        let catch = catch.then(|| (self.rng.chance(1, 2), self.list(depth - 1, scope)));
        let finally = finally.then(|| self.list(depth - 1, scope));
        Stmt::Try {
            block,
            catch,
            finally,
        }
    }

    fn loop_statement(&mut self, depth: usize, scope: &Scope, attached: &[usize]) -> Stmt {
        let kind = LoopKind::ALL[self.rng.below(LoopKind::ALL.len())];
        let mut inner = scope.clone();
        inner.in_loop = true;
        inner.in_breakable = true;
        inner.loop_labels.extend_from_slice(attached);
        Stmt::Loop(kind, Box::new(self.stmt(depth - 1, &inner)))
    }

    fn labelled(&mut self, depth: usize, scope: &Scope) -> Stmt {
        let count = 1 + usize::from(self.rng.chance(1, 4));
        let labels: Vec<usize> = (0..count)
            .map(|_| {
                self.next_label += 1;
                self.next_label
            })
            .collect();
        let mut inner = scope.clone();
        inner.labels.extend_from_slice(&labels);
        let mut stmt = if self.rng.chance(1, 2) {
            self.loop_statement(depth, &inner, &labels)
        } else {
            self.stmt(depth - 1, &inner)
        };
        for &label in labels.iter().rev() {
            stmt = Stmt::Labelled(label, Box::new(stmt));
        }
        stmt
    }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

fn check(programs: impl Iterator<Item = Vec<Stmt>>) -> usize {
    let mut count = 0;
    let mut differences = Vec::new();
    for body in programs {
        count += 1;
        if let Some(difference) = difference(&body) {
            differences.push(difference);
            if differences.len() >= 12 {
                break;
            }
        }
    }
    assert!(
        differences.is_empty(),
        "{} programs differ:\n{}",
        differences.len(),
        differences.join("\n")
    );
    count
}

/// Random statement trees of bounded depth. The reference was also compared
/// with a JavaScript engine: thousands of such programs, run with random
/// choices, return only reads that the reference reaches.
#[test]
fn generated_programs_follow_completion_records() {
    let mut count = 0;
    for seed in 0..4000u64 {
        let mut generator = Generator::new(seed);
        let scope = Scope::default();
        let depth = 2 + (seed % 3) as usize;
        let program = generator.list(depth, &scope);
        count += check(std::iter::once(program));
    }
    assert_eq!(count, 4000);
}

/// Every combination of a context, the three parts of a `try` statement and
/// the statement after it, over a fixed set of leaves.
#[test]
fn try_statement_combinations_follow_completion_records() {
    // The leaves that each part can hold. `Read(k)` is `return process.env.Rk`.
    fn leaves(read: usize) -> Vec<Vec<Stmt>> {
        vec![
            vec![],
            vec![Stmt::ReturnRead(read)],
            vec![Stmt::ReturnLiteral],
            vec![Stmt::Throw],
            vec![Stmt::Break(None)],
            vec![Stmt::Continue(None)],
            vec![Stmt::Break(Some(1))],
            vec![Stmt::Continue(Some(1))],
            vec![Stmt::Call],
            vec![Stmt::If(Box::new(Stmt::Break(Some(1))), None)],
            vec![Stmt::If(
                Box::new(Stmt::ReturnLiteral),
                Some(Box::new(Stmt::Throw)),
            )],
            vec![Stmt::Call, Stmt::Break(None)],
        ]
    }

    // Wrap the statement under test so that `break`, `continue` and the label
    // `L1` are valid and the code after the statement is observable.
    fn contexts(inner: Stmt) -> Vec<Vec<Stmt>> {
        let tail = Stmt::ReturnRead(99);
        let labelled_loop = |kind| {
            Stmt::Labelled(
                1,
                Box::new(Stmt::Loop(
                    kind,
                    Box::new(Stmt::Block(vec![inner.clone(), tail.clone()])),
                )),
            )
        };
        let mut programs = Vec::new();
        for kind in LoopKind::ALL {
            programs.push(vec![labelled_loop(kind)]);
        }
        // A labelled block has no loop to `continue` or to `break` out of.
        if !any(&inner, &|stmt| {
            matches!(
                stmt,
                Stmt::Break(None) | Stmt::Continue(None) | Stmt::Continue(Some(_))
            )
        }) {
            programs.push(vec![Stmt::Labelled(
                1,
                Box::new(Stmt::Block(vec![inner.clone(), tail.clone()])),
            )]);
        }
        programs.push(vec![Stmt::Labelled(
            1,
            Box::new(Stmt::Loop(
                LoopKind::While,
                Box::new(Stmt::Switch(vec![
                    (Clause::Case, vec![inner.clone(), tail.clone()]),
                    (Clause::Default, vec![]),
                ])),
            )),
        )]);
        programs.push(vec![Stmt::Labelled(
            1,
            Box::new(Stmt::Loop(
                LoopKind::ForEver,
                Box::new(Stmt::Try {
                    block: vec![Stmt::ReturnRead(98)],
                    catch: None,
                    finally: Some(vec![inner.clone()]),
                }),
            )),
        )]);
        programs.push(vec![
            Stmt::Labelled(
                1,
                Box::new(Stmt::Loop(
                    LoopKind::While,
                    Box::new(Stmt::Block(vec![inner.clone()])),
                )),
            ),
            tail.clone(),
        ]);
        programs.push(vec![
            Stmt::Try {
                block: vec![],
                catch: Some((true, vec![tail.clone()])),
                finally: None,
            },
            Stmt::Labelled(
                1,
                Box::new(Stmt::Loop(
                    LoopKind::ForEver,
                    Box::new(Stmt::Block(vec![inner.clone()])),
                )),
            ),
            tail,
        ]);
        programs
    }

    let parts = leaves(1);
    let mut count = 0;
    for block in &parts {
        for catch in std::iter::once(None).chain(leaves(2).into_iter().map(Some)) {
            for finally in std::iter::once(None).chain(leaves(3).into_iter().map(Some)) {
                if catch.is_none() && finally.is_none() {
                    continue;
                }
                let inner = Stmt::Try {
                    block: block.clone(),
                    catch: catch.clone().map(|list| (true, list)),
                    finally: finally.clone(),
                };
                count += check(contexts(inner).into_iter());
            }
        }
    }
    assert!(count > 10_000, "{count} programs");
}

/// Statements that nest a `try` in each other, the shape of the reported
/// defects: a transfer pending in a `try` block while a `finally` block runs
/// its own `try`, `switch` or labelled statement.
#[test]
fn nested_try_combinations_follow_completion_records() {
    let leaves = [
        vec![],
        vec![Stmt::Throw],
        vec![Stmt::Break(Some(1))],
        vec![Stmt::Break(Some(2))],
        vec![Stmt::Continue(None)],
        vec![Stmt::ReturnLiteral],
        vec![Stmt::Call],
        vec![Stmt::If(Box::new(Stmt::Throw), None)],
    ];
    let wrap = |inner: Vec<Stmt>, shape: usize| -> Vec<Stmt> {
        match shape {
            0 => inner,
            1 => vec![Stmt::Labelled(2, Box::new(Stmt::Block(inner)))],
            2 => vec![Stmt::Switch(vec![(Clause::Default, inner)])],
            3 => vec![Stmt::Loop(LoopKind::While, Box::new(Stmt::Block(inner)))],
            _ => vec![Stmt::Try {
                block: inner,
                catch: Some((false, vec![])),
                finally: None,
            }],
        }
    };
    let mut count = 0;
    for outer_finally_shape in 0..5 {
        for inner_block in &leaves {
            for inner_catch in std::iter::once(None).chain(leaves.iter().map(Some)) {
                for inner_finally in std::iter::once(None).chain(leaves.iter().map(Some)) {
                    if inner_catch.is_none() && inner_finally.is_none() {
                        continue;
                    }
                    let inner = Stmt::Try {
                        block: inner_block.clone(),
                        catch: inner_catch.map(|list| (true, list.clone())),
                        finally: inner_finally.cloned(),
                    };
                    // `break L2` needs the labelled block of shape 1.
                    if outer_finally_shape != 1
                        && any(&inner, &|stmt| matches!(stmt, Stmt::Break(Some(2))))
                    {
                        continue;
                    }
                    let program = vec![
                        Stmt::Labelled(
                            1,
                            Box::new(Stmt::Loop(
                                LoopKind::ForEver,
                                Box::new(Stmt::Try {
                                    block: vec![Stmt::ReturnRead(1)],
                                    catch: None,
                                    finally: Some(wrap(vec![inner], outer_finally_shape)),
                                }),
                            )),
                        ),
                        Stmt::ReturnRead(2),
                    ];
                    count += check(std::iter::once(program));
                }
            }
        }
    }
    assert!(count > 1_000, "{count} programs");
}

/// The reference agrees with the rows of the documented rules.
#[test]
fn reference_semantics_match_known_completions() {
    let reads = |body: Vec<Stmt>| {
        let mut set: Vec<_> = returnable_reads(&body).into_iter().collect();
        set.sort_unstable();
        set
    };
    // `finally` that always throws replaces the return.
    assert_eq!(
        reads(vec![Stmt::Try {
            block: vec![Stmt::ReturnRead(1)],
            catch: None,
            finally: Some(vec![Stmt::Throw]),
        }]),
        Vec::<usize>::new()
    );
    // `finally` that may complete normally lets the return resume.
    assert_eq!(
        reads(vec![Stmt::Try {
            block: vec![Stmt::ReturnRead(1)],
            catch: None,
            finally: Some(vec![Stmt::If(Box::new(Stmt::Throw), None)]),
        }]),
        vec![1]
    );
    // A `catch` clause is skipped when the `try` block cannot throw.
    assert_eq!(
        reads(vec![Stmt::Try {
            block: vec![Stmt::ReturnBare],
            catch: Some((true, vec![Stmt::ReturnRead(1)])),
            finally: None,
        }]),
        Vec::<usize>::new()
    );
    // The code after an endless loop is unreachable.
    assert_eq!(
        reads(vec![
            Stmt::Loop(LoopKind::ForEver, Box::new(Stmt::Empty)),
            Stmt::ReturnRead(1)
        ]),
        Vec::<usize>::new()
    );
    // A labelled `break` ends the labelled statement normally.
    assert_eq!(
        reads(vec![
            Stmt::Labelled(1, Box::new(Stmt::Block(vec![Stmt::Break(Some(1))]))),
            Stmt::ReturnRead(1)
        ]),
        vec![1]
    );
}
