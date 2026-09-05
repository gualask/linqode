package home

// Where the screen's parts go. Kept apart from the rendering, and free of the
// model, so it can be checked at sizes no test terminal has: the arithmetic
// that decides whether a panel fits is exactly the arithmetic that, when it is
// wrong by one, wraps a line and shifts everything below it down a row.

// The header is two rows without a host sample — the session on a line of its
// own, and a blank one before the body — and three with one, where those two
// rows and the band become a single box.
//
// The box is the reason the arithmetic did not change when it arrived: a
// border costs two rows, and the title line and the blank rule under it were
// two rows already. What was a bare row of meters under a heading is now a
// panel like the others, for nothing.
const (
	titleLines = 1
	blankLines = 1
	footerLine = 1

	// headerHeight is the boxed header: two border rows and the band between
	// them.
	headerHeight = 3
)

type box struct {
	width, height int
}

// content is the area inside a panel's border: what the panel itself is given
// as its size, so no feature package has to know a border costs two cells.
func (b box) content() box {
	return box{width: max(b.width-2, 0), height: max(b.height-2, 0)}
}

// eventsBox is the satellite's height, borders included: four rows of feed
// under a titled rule. Four is what makes it worth drawing — a restart is a
// `die` and a `start`, so anything less cannot show one whole thing that
// happened — and more would be taken from the table, which is the anchor.
const eventsBox = 6

// anchorFloor is the smallest the services table may be squeezed to before
// the satellite gives up its place: two border rows, the heading band, and
// five services. A table showing two rows of six is not a table.
const anchorFloor = 8

type frame struct {
	// band reports whether the host meters get their row. They are one line
	// and the first thing an operator reads, so they are given up only when
	// there is no sample to draw — never to buy space for a panel.
	band bool
	// body is what the anchor panel gets, borders included.
	body box
	// events is the satellite under it, zero-height when the terminal is too
	// short to hold both. Satellites degrade first; the anchor never does.
	events box
}

// layoutFor divides the terminal. An unknown size — before the first
// WindowSizeMsg — leaves the body unbounded, which is how these views behave
// until they are told how much room they have.
//
// events says whether there is a feed panel to place at all. It is not a
// question of whether anything has happened: an empty feed panel says the
// daemon is being watched, which is a thing worth knowing, and a panel that
// appeared the first time a container died would move the table under the
// operator at the worst possible moment.
func layoutFor(width, height int, band, events bool) frame {
	if width <= 0 || height <= 0 {
		return frame{band: band}
	}
	header := titleLines + blankLines
	if band {
		header++
	}
	body := box{width: width, height: max(height-header-footerLine, 0)}
	if !events || body.height < anchorFloor+eventsBox {
		return frame{band: band, body: body}
	}
	return frame{
		band:   band,
		body:   box{width: width, height: body.height - eventsBox},
		events: box{width: width, height: eventsBox},
	}
}
