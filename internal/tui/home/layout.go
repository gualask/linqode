package home

// Where the screen's parts go. Kept apart from the rendering, and free of the
// model, so it can be checked at sizes no test terminal has: the arithmetic
// that decides whether a panel fits is exactly the arithmetic that, when it is
// wrong by one, wraps a line and shifts everything below it down a row.

// The header is the title line, the host band when there is a sample, and a
// blank line before the body — a blank line rather than a rule, because the
// panels below draw their own borders and a rule here would be a second
// separator stacked on the first.
const (
	titleLines = 1
	blankLines = 1
	footerLine = 1
)

type box struct {
	width, height int
}

// content is the area inside a panel's border: what the panel itself is given
// as its size, so no feature package has to know a border costs two cells.
func (b box) content() box {
	return box{width: max(b.width-2, 0), height: max(b.height-2, 0)}
}

type frame struct {
	// band reports whether the host meters get their row. They are one line
	// and the first thing an operator reads, so they are given up only when
	// there is no sample to draw — never to buy space for a panel.
	band bool
	// body is what the panels share, borders included.
	body box
}

// layoutFor divides the terminal. An unknown size — before the first
// WindowSizeMsg — leaves the body unbounded, which is how these views behave
// until they are told how much room they have.
func layoutFor(width, height int, band bool) frame {
	if width <= 0 || height <= 0 {
		return frame{band: band}
	}
	header := titleLines + blankLines
	if band {
		header++
	}
	return frame{
		band: band,
		body: box{width: width, height: max(height-header-footerLine, 0)},
	}
}
