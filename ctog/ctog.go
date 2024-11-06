// Package ctog contains the logic to convert a CII document into a GOBL envelope
package ctog

import (
	"encoding/xml"

	"github.com/invopop/gobl"
	"github.com/invopop/gobl/bill"
	"github.com/invopop/gobl/catalogues/untdid"
	"github.com/invopop/gobl/cbc"
	"github.com/invopop/gobl/currency"
	"github.com/invopop/gobl/org"
	"github.com/invopop/gobl/tax"
)

// Converter is a struct that contains the necessary elements to convert between GOBL and CII
type Converter struct {
	// CtoG Output
	inv *bill.Invoice
	// CtoG Input
	doc *Document
}

// NewConverter Builder function
func NewConverter() *Converter {
	c := new(Converter)
	c.inv = new(bill.Invoice)
	c.doc = new(Document)
	return c
}

// GetInvoice returns the invoice from the converter
func (c *Converter) GetInvoice() *bill.Invoice {
	return c.inv
}

// ConvertToGOBL converts a CII document into a GOBL envelope
func (c *Converter) ConvertToGOBL(xmlData []byte) (*gobl.Envelope, error) {
	if err := xml.Unmarshal(xmlData, &c.doc); err != nil {
		return nil, err
	}

	err := c.NewInvoice(c.doc)
	if err != nil {
		return nil, err
	}

	env, err := gobl.Envelop(c.inv)
	if err != nil {
		return nil, err
	}
	return env, nil
}

// NewInvoice creates a new GOBL invoice from a CII document
func (c *Converter) NewInvoice(doc *Document) error {

	c.inv = &bill.Invoice{
		Code:     cbc.Code(doc.ExchangedDocument.ID),
		Type:     TypeCodeParse(doc.ExchangedDocument.TypeCode),
		Currency: currency.Code(doc.SupplyChainTradeTransaction.ApplicableHeaderTradeSettlement.InvoiceCurrencyCode),
		Supplier: c.getParty(&doc.SupplyChainTradeTransaction.ApplicableHeaderTradeAgreement.SellerTradeParty),
		Customer: c.getParty(&doc.SupplyChainTradeTransaction.ApplicableHeaderTradeAgreement.BuyerTradeParty),
		Tax: &bill.Tax{
			Ext: tax.Extensions{
				untdid.ExtKeyDocumentType: tax.ExtValue(doc.ExchangedDocument.TypeCode),
			},
		},
	}

	issueDate, err := ParseDate(doc.ExchangedDocument.IssueDateTime.DateTimeString.Value)
	if err != nil {
		return err
	}
	c.inv.IssueDate = issueDate

	err = c.prepareLines(&doc.SupplyChainTradeTransaction)
	if err != nil {
		return err
	}

	// Payment comprised of terms, means and payee. Check tehre is relevant info in at least one of them to create a payment
	ahts := &doc.SupplyChainTradeTransaction.ApplicableHeaderTradeSettlement
	if ahts.hasPayment() {
		err = c.preparePayment(ahts)
		if err != nil {
			return err
		}
	}

	if len(doc.ExchangedDocument.IncludedNote) > 0 {
		c.inv.Notes = make([]*cbc.Note, 0, len(doc.ExchangedDocument.IncludedNote))
		for _, note := range doc.ExchangedDocument.IncludedNote {
			n := &cbc.Note{
				Text: note.Content,
			}
			if note.SubjectCode != "" {
				n.Code = note.SubjectCode
			}
			c.inv.Notes = append(c.inv.Notes, n)
		}
	}

	err = c.prepareOrdering(doc)
	if err != nil {
		return err
	}

	err = c.prepareDelivery(doc)
	if err != nil {
		return err
	}

	if len(ahts.InvoiceReferencedDcument) > 0 {
		c.inv.Preceding = make([]*org.DocumentRef, 0, len(ahts.InvoiceReferencedDcument))
		for _, ref := range ahts.InvoiceReferencedDcument {
			docRef := &org.DocumentRef{
				Code: cbc.Code(ref.IssuerAssignedID),
			}
			if ref.FormattedIssueDateTime != nil {
				refDate, err := ParseDate(ref.FormattedIssueDateTime.DateTimeString.Value)
				if err != nil {
					return err
				}
				docRef.IssueDate = &refDate
			}
			c.inv.Preceding = append(c.inv.Preceding, docRef)
		}
	}

	if doc.SupplyChainTradeTransaction.ApplicableHeaderTradeAgreement.SellerTaxRepresentativeTradeParty != nil {
		// Move the original seller to the ordering.seller party
		if c.inv.Ordering == nil {
			c.inv.Ordering = &bill.Ordering{}
		}
		c.inv.Ordering.Seller = c.inv.Supplier

		// Overwrite the seller field with the tax representative
		c.inv.Supplier = c.getParty(doc.SupplyChainTradeTransaction.ApplicableHeaderTradeAgreement.SellerTaxRepresentativeTradeParty)
	}

	if len(ahts.SpecifiedTradeAllowanceCharge) > 0 {
		err = c.prepareChargesAndDiscounts(ahts)
		if err != nil {
			return err
		}
	}
	return nil
}

func (ah *ApplicableHeaderTradeSettlement) hasPayment() bool {
	return ah.PayeeTradeParty != nil ||
		(len(ah.SpecifiedTradePaymentTerms) > 0 && ah.SpecifiedTradePaymentTerms[0].DueDateDateTime != nil) ||
		(len(ah.SpecifiedTradeSettlementPaymentMeans) > 0 && ah.SpecifiedTradeSettlementPaymentMeans[0].TypeCode != "1")
}
